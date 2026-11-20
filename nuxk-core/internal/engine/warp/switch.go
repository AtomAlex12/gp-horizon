// Package warp manages the transports of the one logical WARP engine.
package warp

import (
	"context"
	"errors"
	"fmt"
	"time"

	"nuxk.dev/horizon/core/internal/engine"
)

type switchHooks struct {
	// prepare validates both backends and guards/detaches the old route. It
	// must leave the previous binding in place if it returns an error.
	prepare func(context.Context) error
	bind    func(context.Context) error
	restore func(context.Context) error
	commit  func() error
}

// switchTransport commits the selection only once the replacement has
// proven connectivity and acquired the old route's responsibilities.
// Its caller serializes it with engine actions and plane reconciliation.
func switchTransport(ctx context.Context, old, next engine.Engine, h switchHooks) error {
	status, err := old.Info(ctx)
	if err != nil {
		return fmt.Errorf("не удалось проверить действующий WARP: %w", err)
	}
	if err := h.prepare(ctx); err != nil {
		return err
	}
	nextAttempted := false
	rollback := func(cause error) error {
		// The user's request may already have timed out. Give recovery its
		// own bounded context and report every failed recovery step.
		recovery, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		var failures []error
		if nextAttempted {
			if err := stopAndCheck(recovery, next); err != nil {
				return errors.Join(cause, fmt.Errorf("откат WARP остановлен: новый движок не остановился: %w", err))
			}
		}
		if status.Running {
			now, infoErr := old.Info(recovery)
			if infoErr != nil || !now.Running {
				if err := old.Start(recovery); err != nil {
					failures = append(failures, fmt.Errorf("запуск прежнего WARP: %w", err))
				}
			}
		}
		if err := h.restore(recovery); err != nil {
			failures = append(failures, fmt.Errorf("восстановление маршрутов WARP: %w", err))
		}
		if len(failures) != 0 {
			return errors.Join(cause, fmt.Errorf("откат WARP завершился с ошибками: %w", errors.Join(failures...)))
		}
		return cause
	}
	if status.Running {
		if err := stopAndCheck(ctx, old); err != nil {
			return rollback(fmt.Errorf("остановка прежнего WARP: %w", err))
		}
	}
	nextAttempted = true
	if err := next.Start(ctx); err != nil {
		return rollback(fmt.Errorf("запуск нового WARP: %w", err))
	}
	probe, err := next.Probe(ctx, nil)
	if err != nil {
		return rollback(fmt.Errorf("проверка нового WARP: %w", err))
	}
	if !probe.OK {
		return rollback(fmt.Errorf("новый WARP не прошёл проверку: %s", probe.Reason))
	}
	if err := h.bind(ctx); err != nil {
		return rollback(fmt.Errorf("переназначение маршрутов WARP: %w", err))
	}
	if err := ctx.Err(); err != nil {
		return rollback(err)
	}
	if err := h.commit(); err != nil {
		return rollback(fmt.Errorf("сохранение транспорта WARP: %w", err))
	}
	return nil
}

func stopAndCheck(ctx context.Context, e engine.Engine) error {
	if err := e.Stop(ctx); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tick := time.NewTicker(250 * time.Millisecond)
	defer tick.Stop()
	for {
		info, err := e.Info(ctx)
		if err != nil {
			return err
		}
		if !info.Running {
			return nil
		}
		select {
		case <-ctx.Done():
			return errors.New("процесс WARP продолжает работать после остановки")
		case <-tick.C:
		}
	}
}
