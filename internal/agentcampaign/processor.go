package agentcampaign

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/RainLib/open-review-platform/internal/agentdecision"
	"github.com/RainLib/open-review-platform/internal/agentplan"
	"github.com/RainLib/open-review-platform/internal/agenttasksource"
	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/RainLib/open-review-platform/internal/store"
	"time"
)

type Store interface {
	ClaimAgentCampaignScan(context.Context, string) (domain.AgentCampaign, domain.AgentCampaignTarget, error)
	FinishAgentCampaignScan(context.Context, string, domain.AgentCampaignTarget, domain.AgentCampaignScan, domain.AgentTaskSourceSnapshot, string) error
	ScheduleAgentCampaignExecutions(context.Context) error
}
type Processor struct {
	Store    Store
	Resolver agenttasksource.Resolver
	Planner  agentplan.Planner
	WorkerID string
}

func (p Processor) RunOnce(ctx context.Context) (bool, error) {
	if err := p.Store.ScheduleAgentCampaignExecutions(ctx); err != nil {
		return false, err
	}
	c, t, err := p.Store.ClaimAgentCampaignScan(ctx, p.WorkerID)
	if errors.Is(err, store.ErrNoQueuedAgentTask) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	work, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	receipt, err := p.Resolver.ScanCampaign(work, t, c.Input)
	snapshot := domain.AgentTaskSourceSnapshot{}
	code := ""
	if err != nil {
		code = "provider_scan_unavailable"
	} else if receipt.Complete && c.Input.Mode != "scan" && len(receipt.Files) > 0 && (receipt.Matches > 0 || c.Input.Search == "") {
		t.Scan = receipt
		b := store.CampaignBinding(c, t)
		snapshot = domain.AgentTaskSourceSnapshot{BaseRef: receipt.BaseRef, BaseSHA: receipt.BaseSHA, Campaign: &b}
		preview := receipt
		if len(preview.Files) > 30 {
			preview.Files = preview.Files[:30]
		}
		raw, _ := json.Marshal(preview)
		snapshot.RepositoryEvidence = "Complete provider-read text scan at frozen commit. Excluded binary, symlink and credential paths are counted.\n" + string(raw) + "\nFull retained scan receipt SHA256: " + domain.CampaignDigest(receipt)
		snapshot.DecisionSignal, err = agentdecision.EvaluateSnapshot(work, t.PlanningTask, snapshot)
		if err != nil {
			code = "campaign_decision_unavailable"
		} else {
			plan, e := p.Planner.Generate(work, t.PlanningTask, snapshot)
			if e != nil {
				code = "campaign_plan_unavailable"
			} else {
				snapshot.GeneratedPlan = &plan
			}
		}
	}
	finishErr := p.Store.FinishAgentCampaignScan(ctx, p.WorkerID, t, receipt, snapshot, code)
	return true, finishErr
}
