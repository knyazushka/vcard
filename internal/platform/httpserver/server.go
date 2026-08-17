// Package httpserver — обёртка над net/http с корректной остановкой.
package httpserver

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"
)

// Server — HTTP-слушатель, умеющий останавливаться по контексту.
type Server struct {
	srv             *http.Server
	shutdownTimeout time.Duration
	log             *slog.Logger
	name            string
}

// Options — параметры слушателя.
type Options struct {
	Addr            string
	Handler         http.Handler
	ReadTimeout     time.Duration
	WriteTimeout    time.Duration
	IdleTimeout     time.Duration
	ShutdownTimeout time.Duration
}

// New создаёт слушатель; name попадает в логи и различает api и admin.
func New(name string, log *slog.Logger, o Options) *Server {
	return &Server{
		name:            name,
		log:             log,
		shutdownTimeout: o.ShutdownTimeout,
		srv: &http.Server{
			Addr:              o.Addr,
			Handler:           o.Handler,
			ReadHeaderTimeout: o.ReadTimeout,
			ReadTimeout:       o.ReadTimeout,
			WriteTimeout:      o.WriteTimeout,
			IdleTimeout:       o.IdleTimeout,
		},
	}
}

// Run слушает порт и останавливается по отмене контекста.
//
// Порт биндится самим процессом, без внешнего веб-сервера внутри контейнера —
// фактор VII. Остановка корректная: сначала перестаём принимать новые
// соединения, потом даём доработать текущим, и только по истечении таймаута
// рвём принудительно — фактор IX.
func (s *Server) Run(ctx context.Context) error {
	errCh := make(chan error, 1)

	go func() {
		s.log.Info("http server started", "name", s.name, "addr", s.srv.Addr)
		if err := s.srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}

	s.log.Info("http server stopping", "name", s.name)

	// Контекст остановки отвязан от родительского: тот уже отменён,
	// иначе мы бы сюда не попали.
	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), s.shutdownTimeout)
	defer cancel()

	if err := s.srv.Shutdown(shutdownCtx); err != nil {
		s.log.Error("graceful shutdown failed, closing forcefully",
			"name", s.name, "error", err)
		return s.srv.Close()
	}

	s.log.Info("http server stopped", "name", s.name)
	return nil
}
