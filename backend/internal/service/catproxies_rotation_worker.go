package service

import (
	"context"
	"database/sql"
	"log"
	"sync"
	"time"

	"github.com/google/uuid"
)

const (
	catProxiesRotationInterval = time.Minute
	catProxiesRotationLockKey  = "jobs:catproxies_rotation"
	catProxiesRotationWorkers  = 5
)

type CatProxiesRotationWorker struct {
	runtime *ManagedProxyRuntimeService
	repo    ManagedProxyRuntimeRepository
	lock    LeaderLockCache
	db      *sql.DB
	owner   string
	stop    chan struct{}
	once    sync.Once
	wg      sync.WaitGroup
}

func ProvideCatProxiesRotationWorker(runtime *ManagedProxyRuntimeService, repo ManagedProxyRuntimeRepository, lock LeaderLockCache, db *sql.DB) *CatProxiesRotationWorker {
	worker := &CatProxiesRotationWorker{runtime: runtime, repo: repo, lock: lock, db: db, owner: uuid.NewString(), stop: make(chan struct{})}
	worker.Start()
	return worker
}

func (w *CatProxiesRotationWorker) Start() {
	if w == nil || w.runtime == nil || w.repo == nil {
		return
	}
	w.wg.Add(1)
	go func() {
		defer w.wg.Done()
		ticker := time.NewTicker(catProxiesRotationInterval)
		defer ticker.Stop()
		w.runOnce()
		for {
			select {
			case <-ticker.C:
				w.runOnce()
			case <-w.stop:
				return
			}
		}
	}()
}

func (w *CatProxiesRotationWorker) Stop() {
	if w == nil {
		return
	}
	w.once.Do(func() { close(w.stop) })
	w.wg.Wait()
}

func (w *CatProxiesRotationWorker) runOnce() {
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Second)
	defer cancel()
	release, acquired := tryAcquireSingletonLeaderLock(ctx, w.lock, w.db, catProxiesRotationLockKey, w.owner, 55*time.Second)
	if !acquired {
		return
	}
	defer release()
	due, err := w.repo.ListDue(ctx, time.Now(), 100)
	if err != nil {
		log.Printf("[CatProxiesRotation] list due leases failed: %v", err)
		return
	}
	type rotationJob struct {
		accountID  int64
		providerID int64
	}
	jobs := make(chan rotationJob)
	var workers sync.WaitGroup
	for range catProxiesRotationWorkers {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for job := range jobs {
				if _, err := w.runtime.RotateDue(ctx, job.accountID); err != nil {
					log.Printf("[CatProxiesRotation] account_id=%d provider_id=%d reason=scheduled result=failed error=%v", job.accountID, job.providerID, err)
					continue
				}
				log.Printf("[CatProxiesRotation] account_id=%d provider_id=%d reason=scheduled result=succeeded", job.accountID, job.providerID)
			}
		}()
	}
	for _, item := range due {
		select {
		case jobs <- rotationJob{accountID: item.Lease.AccountID, providerID: item.Lease.ProviderConfigID}:
		case <-ctx.Done():
			close(jobs)
			workers.Wait()
			return
		}
	}
	close(jobs)
	workers.Wait()
}
