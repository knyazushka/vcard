// Package worker содержит фоновые задачи процесса.
package worker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/knyazushka/vcard/internal/email"
	"github.com/knyazushka/vcard/internal/repository/postgres"
)

// OutboxQueue — то, что рассыльщику нужно от очереди.
type OutboxQueue interface {
	Claim(ctx context.Context, limit int, leaseFor time.Duration) ([]postgres.OutboxItem, error)
	MarkSent(ctx context.Context, id uuid.UUID) error
	MarkFailed(ctx context.Context, id uuid.UUID, cause string, retryIn time.Duration, maxAttempts int16) error
}

// Outbox разгребает очередь исходящих писем.
type Outbox struct {
	queue    OutboxQueue
	sender   email.Sender
	renderer *email.Renderer
	log      *slog.Logger

	interval  time.Duration
	batchSize int
}

// maxAttempts ограничивает число попыток: несуществующий ящик будет
// отвергаться вечно, и без потолка очередь растёт, а логи заполняются
// одной и той же ошибкой.
const maxAttempts int16 = 6

// NewOutbox создаёт рассыльщика.
func NewOutbox(
	queue OutboxQueue, sender email.Sender, renderer *email.Renderer,
	log *slog.Logger, interval time.Duration, batchSize int,
) *Outbox {
	return &Outbox{
		queue:     queue,
		sender:    sender,
		renderer:  renderer,
		log:       log,
		interval:  interval,
		batchSize: batchSize,
	}
}

// Run крутит цикл до отмены контекста.
//
// Живёт в том же процессе, что и API: письма отсюда идут редко, отдельный
// деплой ради этого — лишняя движущаяся часть. Когда объём вырастет,
// тот же код запустится отдельной командой без изменений — очередь
// в базе, а не в памяти.
func (o *Outbox) Run(ctx context.Context) error {
	ticker := time.NewTicker(o.interval)
	defer ticker.Stop()

	o.log.Info("outbox worker started", "interval", o.interval, "batch", o.batchSize)

	for {
		select {
		case <-ctx.Done():
			o.log.Info("outbox worker stopped")
			return nil
		case <-ticker.C:
			if err := o.tick(ctx); err != nil && !errors.Is(err, context.Canceled) {
				o.log.Error("outbox tick failed", "error", err)
			}
		}
	}
}

func (o *Outbox) tick(ctx context.Context) error {
	// Аренда заметно длиннее интервала опроса: иначе следующий тик заберёт
	// письмо, которое ещё отправляется, и получатель увидит дубль.
	items, err := o.queue.Claim(ctx, o.batchSize, 2*time.Minute)
	if err != nil {
		return fmt.Errorf("claim: %w", err)
	}

	for _, item := range items {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		o.deliver(ctx, item)
	}
	return nil
}

func (o *Outbox) deliver(ctx context.Context, item postgres.OutboxItem) {
	msg, err := o.render(item)
	if err != nil {
		// Письмо не собирается — повторять бессмысленно, состав очереди
		// не изменится. Гасим сразу, чтобы не тратить попытки.
		o.log.Error("outbox message is not renderable",
			"id", item.ID, "kind", item.Kind, "error", err)
		o.fail(ctx, item.ID, err.Error(), time.Hour, 0)
		return
	}

	if err := o.sender.Send(msg); err != nil {
		// Экспоненциальная задержка: недоступный почтовый сервер не должен
		// получать одинаковый поток попыток каждые несколько секунд.
		backoff := time.Duration(1<<min(item.Attempts, 8)) * time.Second

		o.log.Warn("email delivery failed",
			"id", item.ID, "attempt", item.Attempts, "retry_in", backoff, "error", err)
		o.fail(ctx, item.ID, err.Error(), backoff, maxAttempts)
		return
	}

	if err := o.queue.MarkSent(ctx, item.ID); err != nil {
		// Письмо ушло, а отметка не встала — при следующем тике оно уйдёт
		// повторно. Дубль приглашения неприятен, но безопасен: токен
		// в письме тот же самый.
		o.log.Error("email sent but not marked", "id", item.ID, "error", err)
		return
	}

	o.log.Info("email sent", "id", item.ID, "kind", item.Kind)
}

func (o *Outbox) fail(ctx context.Context, id uuid.UUID, cause string, retryIn time.Duration, attempts int16) {
	if err := o.queue.MarkFailed(ctx, id, cause, retryIn, attempts); err != nil {
		o.log.Error("cannot mark outbox item failed", "id", id, "error", err)
	}
}

func (o *Outbox) render(item postgres.OutboxItem) (email.Message, error) {
	switch item.Kind {
	case email.KindInvitation:
		return o.renderer.Invitation(item.To, email.InvitationData{
			CompanyName:  str(item.Payload, "companyName"),
			InviterEmail: str(item.Payload, "inviterEmail"),
			AcceptURL:    str(item.Payload, "acceptUrl"),
			ExpiresAt:    str(item.Payload, "expiresAt"),
			IsAdmin:      str(item.Payload, "role") == "ADMIN",
		})
	default:
		return email.Message{}, fmt.Errorf("unknown message kind %q", item.Kind)
	}
}

func str(m map[string]any, key string) string {
	v, _ := m[key].(string)
	return v
}
