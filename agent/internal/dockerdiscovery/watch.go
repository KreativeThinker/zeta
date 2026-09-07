package dockerdiscovery

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/kreativethinker/zeta/agent/internal/config"
)

const debounce = 500 * time.Millisecond

// Watch re-runs Discover on every container start/stop/die event (debounced)
// and reports the resulting service list to onChange. Blocks until ctx is
// canceled.
func (w *Watcher) Watch(ctx context.Context, onChange func([]config.ZetaService)) {
	for {
		if err := w.streamEvents(ctx, onChange); err != nil && !errors.Is(err, context.Canceled) {
			slog.Warn("dockerdiscovery: event stream error, retrying", "err", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Second):
			}
			continue
		}
		if ctx.Err() != nil {
			return
		}
	}
}

func (w *Watcher) streamEvents(ctx context.Context, onChange func([]config.ZetaService)) error {
	q := "type=container&filters=" + `{"event":["start","stop","die"]}`
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://unix/events?"+q, nil)
	if err != nil {
		return err
	}
	resp, err := w.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("docker /events: status %d", resp.StatusCode)
	}

	var timer *time.Timer
	fire := func() {
		svcs, err := w.Discover(ctx)
		if err != nil {
			slog.Warn("dockerdiscovery: re-discovery failed", "err", err)
			return
		}
		onChange(svcs)
	}

	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		if timer != nil {
			timer.Stop()
		}
		timer = time.AfterFunc(debounce, fire)
	}
	if timer != nil {
		timer.Stop()
	}
	return sc.Err()
}
