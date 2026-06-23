package worker

import (
	"context"
	"log"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/model"
	antigravityinspectionservice "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/antigravityinspection"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
)

type AntigravityInspectionWorker struct {
	store   *store.Store
	service *antigravityinspectionservice.Service
}

func NewAntigravityInspectionWorker(store *store.Store, service *antigravityinspectionservice.Service) *AntigravityInspectionWorker {
	return &AntigravityInspectionWorker{store: store, service: service}
}

func (w *AntigravityInspectionWorker) Start(ctx context.Context) {
	if w == nil || w.service == nil {
		return
	}
	go w.run(ctx)
}

func (w *AntigravityInspectionWorker) run(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	w.tick(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.tick(ctx)
		}
	}
}

func (w *AntigravityInspectionWorker) tick(ctx context.Context) {
	cfg, configured, err := w.service.ResolveConfig(ctx)
	if err != nil {
		log.Printf("resolve antigravity inspection config: %v", err)
		return
	}
	if !configured || cfg.Enabled == nil || !*cfg.Enabled {
		return
	}
	now := time.Now()
	triggerKey := model.AntigravityInspectionTriggerKey(now, cfg)
	if triggerKey == "" || !model.AntigravityInspectionScheduleDue(now, w.lastScheduledRunTime(ctx), cfg) {
		return
	}
	if _, ok, err := w.store.GetLatestAntigravityInspectionRunByTrigger(ctx, model.AntigravityInspectionTriggerScheduled, triggerKey); err != nil {
		log.Printf("load antigravity inspection trigger: %v", err)
		return
	} else if ok {
		return
	}
	go func() {
		if _, err := w.service.Run(ctx, antigravityinspectionservice.RunRequest{
			TriggerType: model.AntigravityInspectionTriggerScheduled,
			TriggerKey:  triggerKey,
		}); err != nil && err != antigravityinspectionservice.ErrRunAlreadyActive {
			log.Printf("run scheduled antigravity inspection: %v", err)
		}
	}()
}

func (w *AntigravityInspectionWorker) lastScheduledRunTime(ctx context.Context) time.Time {
	runs, err := w.store.ListAntigravityInspectionRuns(ctx, 20)
	if err != nil {
		return time.Time{}
	}
	for _, run := range runs {
		if run.TriggerType != model.AntigravityInspectionTriggerScheduled || run.StartedAtMS <= 0 {
			continue
		}
		return time.UnixMilli(run.StartedAtMS)
	}
	return time.Time{}
}
