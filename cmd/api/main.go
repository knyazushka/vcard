// Command api — HTTP-сервис виртуальных визиток.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"golang.org/x/sync/errgroup"

	"github.com/knyazushka/vcard/internal/config"
	"github.com/knyazushka/vcard/internal/email"
	"github.com/knyazushka/vcard/internal/gen/openapi"
	filesrv "github.com/knyazushka/vcard/internal/handler/files"
	apihttp "github.com/knyazushka/vcard/internal/handler/http"
	"github.com/knyazushka/vcard/internal/handler/middleware"
	"github.com/knyazushka/vcard/internal/platform/httpserver"
	"github.com/knyazushka/vcard/internal/platform/logger"
	"github.com/knyazushka/vcard/internal/platform/observability"
	"github.com/knyazushka/vcard/internal/platform/postgres"
	"github.com/knyazushka/vcard/internal/render"
	postgresrepo "github.com/knyazushka/vcard/internal/repository/postgres"
	"github.com/knyazushka/vcard/internal/service/auth"
	"github.com/knyazushka/vcard/internal/service/avatar"
	"github.com/knyazushka/vcard/internal/service/card"
	"github.com/knyazushka/vcard/internal/service/company"
	"github.com/knyazushka/vcard/internal/service/invitation"
	"github.com/knyazushka/vcard/internal/service/profile"
	"github.com/knyazushka/vcard/internal/storage"
	"github.com/knyazushka/vcard/internal/storage/localfs"
	"github.com/knyazushka/vcard/internal/worker"
)

func main() {
	if err := run(); err != nil {
		// Логгера может ещё не быть — конфиг разбирается раньше него.
		_, _ = fmt.Fprintf(os.Stderr, "fatal: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	log := logger.New(cfg.LogLevel, cfg.IsProduction())
	slog.SetDefault(log)

	// Контекст отменяется по SIGINT/SIGTERM — отсюда начинается корректная
	// остановка всего процесса.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	pool, err := postgres.New(ctx, cfg.DB)
	if err != nil {
		return fmt.Errorf("connect database: %w", err)
	}
	defer pool.Close()

	log.Info("connected to database")

	tokens := auth.NewTokenIssuer(cfg.Auth.JWTSecret, cfg.Auth.AccessTTL, cfg.Auth.RefreshTTL)

	files, err := localfs.New(cfg.Storage.LocalPath, cfg.Storage.BaseURL)
	if err != nil {
		return fmt.Errorf("init storage: %w", err)
	}

	users := postgresrepo.NewUserRepo(pool)
	hasher := auth.NewHasher(cfg.Auth.ArgonMemoryKiB, cfg.Auth.ArgonTime, cfg.Auth.ArgonThreads)

	authService := auth.NewService(users, postgresrepo.NewSessionRepo(pool), hasher, tokens, log)
	companyService := company.NewService(postgresrepo.NewCompanyRepo(pool), users)
	profileService := profile.NewService(postgresrepo.NewProfileRepo(pool), companyService)
	invitationService := invitation.NewService(
		postgresrepo.NewInvitationRepo(pool),
		users,
		companyService,
		hasher,
		cfg.Public.AppURL,
		cfg.Public.InvitationTTL,
	)

	cardRenderer, err := render.New()
	if err != nil {
		return fmt.Errorf("init card renderer: %w", err)
	}
	cardService := card.NewService(postgresrepo.NewProfileRepo(pool), files, cardRenderer)
	// Нарезка аватара по кропу для публичной страницы: без неё карточка
	// и страница показывают одно лицо по-разному.
	avatarService := avatar.NewService(files)

	renderer, err := email.NewRenderer()
	if err != nil {
		return fmt.Errorf("parse email templates: %w", err)
	}

	outbox := worker.NewOutbox(
		postgresrepo.NewOutboxRepo(pool),
		email.NewSMTPSender(cfg.Mail.SMTPAddr, cfg.Mail.From, cfg.Mail.SMTPUser, cfg.Mail.SMTPPass),
		renderer, log, cfg.Mail.OutboxPollInterval, cfg.Mail.OutboxBatchSize,
	)

	apiHandler, err := buildAPIHandler(cfg, log, files, apihttp.Deps{
		Auth:          authService,
		Companies:     companyService,
		Invitations:   invitationService,
		Profiles:      profileService,
		Cards:         cardService,
		Avatars:       avatarService,
		Files:         files,
		Limits:        apihttp.Limits{Avatar: cfg.Storage.MaxAvatarBytes, Logo: cfg.Storage.MaxLogoBytes},
		Log:           log,
		SecureCookies: cfg.HTTP.CookieSecure,
	}, tokens)
	if err != nil {
		return err
	}

	apiSrv := httpserver.New("api", log, httpserver.Options{
		Addr:            cfg.HTTP.Addr,
		Handler:         apiHandler,
		ReadTimeout:     cfg.HTTP.ReadTimeout,
		WriteTimeout:    cfg.HTTP.WriteTimeout,
		IdleTimeout:     cfg.HTTP.IdleTimeout,
		ShutdownTimeout: cfg.HTTP.ShutdownTimeout,
	})

	adminSrv := httpserver.New("admin", log, httpserver.Options{
		Addr:            cfg.HTTP.AdminAddr,
		Handler:         observability.NewAdminHandler(pool),
		ReadTimeout:     cfg.HTTP.ReadTimeout,
		WriteTimeout:    cfg.HTTP.WriteTimeout,
		IdleTimeout:     cfg.HTTP.IdleTimeout,
		ShutdownTimeout: cfg.HTTP.ShutdownTimeout,
	})

	// Оба сервера останавливаются вместе: errgroup отменяет общий контекст,
	// как только падает любой из них.
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error { return apiSrv.Run(gctx) })
	g.Go(func() error { return adminSrv.Run(gctx) })
	// Рассыльщик живёт в том же процессе: писем немного, а отдельный деплой
	// ради них — лишняя движущаяся часть. Очередь лежит в базе, поэтому
	// вынести его в свою команду можно будет без изменений в коде.
	g.Go(func() error { return outbox.Run(gctx) })

	if err := g.Wait(); err != nil {
		return fmt.Errorf("server: %w", err)
	}

	log.Info("shutdown complete")
	return nil
}

func buildAPIHandler(
	cfg *config.Config,
	log *slog.Logger,
	blobs storage.BlobStore,
	deps apihttp.Deps,
	tokens *auth.TokenIssuer,
) (http.Handler, error) {
	srv, err := openapi.NewServer(
		apihttp.NewAPI(deps),
		apihttp.NewSecurity(tokens),
		openapi.WithPathPrefix("/api/v1"),
		openapi.WithErrorHandler(apihttp.NewErrorHandler(log)),
	)
	if err != nil {
		return nil, fmt.Errorf("build openapi server: %w", err)
	}

	// Раздача загруженных файлов живёт рядом с API, но вне сгенерированного
	// роутера: в спеке её нет, это доставка статики. Ссылки на неё уже
	// уходят клиенту в ответах — без этого маршрута они вели бы в никуда.
	mux := http.NewServeMux()
	mux.Handle("/files/", filesrv.New("files", blobs, log))
	mux.Handle("/", srv)

	// Порядок важен: RequestID должен идти первым, чтобы идентификатор
	// был доступен и логу, и обработчику паники.
	return middleware.Chain(mux,
		middleware.RequestID(cfg.HTTP.TrustProxyHeaders),
		middleware.Recover(log),
		middleware.AccessLog(log),
		middleware.ClientInfo(cfg.HTTP.TrustProxyHeaders),
		// Ниже логирования: в журнал должен попадать реальный код ответа,
		// в том числе 304.
		middleware.ConditionalGet(),
	), nil
}
