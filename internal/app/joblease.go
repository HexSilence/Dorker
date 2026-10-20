package app

import (
	"context"
	"sync"
	"time"

	"github.com/HexSilence/dorker/internal/store"
)

const (
	jobLeaseTTL       = 30 * time.Second
	jobHeartbeatEvery = 10 * time.Second
)

type jobLease struct {
	cancel context.CancelFunc
	stop   chan struct{}
	done   chan struct{}
	once   sync.Once
	mu     sync.Mutex
	err    error
}

func acquireJobLease(parent context.Context, st *store.Store, job string) (context.Context, string, *jobLease, error) {
	owner := newID()
	if err := st.AcquireJob(parent, job, owner, time.Now(), jobLeaseTTL); err != nil {
		return nil, "", nil, err
	}
	ctx, cancel := context.WithCancel(parent)
	l := &jobLease{cancel: cancel, stop: make(chan struct{}), done: make(chan struct{})}
	go func() {
		defer close(l.done)
		ticker := time.NewTicker(jobHeartbeatEvery)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-l.stop:
				return
			case now := <-ticker.C:
				if err := st.HeartbeatJob(context.Background(), job, owner, now, jobLeaseTTL); err != nil {
					l.mu.Lock()
					l.err = err
					l.mu.Unlock()
					cancel()
					return
				}
			}
		}
	}()
	return ctx, owner, l, nil
}

func (l *jobLease) Stop() error {
	l.once.Do(func() { close(l.stop) })
	<-l.done
	l.cancel()
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.err
}
