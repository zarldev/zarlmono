package grpc

import (
	"context"
	"fmt"

	"connectrpc.com/connect"
	"github.com/zarldev/zarlmono/zarlai/repository"
	zarlv1 "github.com/zarldev/zarlmono/zarlai/transport/grpc/gen/zarl/v1"
	"github.com/zarldev/zarlmono/zkit/skills"
)

func (a *AdminServer) ListSkills(ctx context.Context, req *connect.Request[zarlv1.ListSkillsRequest]) (*connect.Response[zarlv1.ListSkillsResponse], error) {
	if a.skills == nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("skills not configured"))
	}
	rows, err := a.skills.List(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("list skills: %w", err))
	}
	out := make([]*zarlv1.SkillMsg, len(rows))
	for i, r := range rows {
		out[i] = skillToProto(r)
	}
	return connect.NewResponse(&zarlv1.ListSkillsResponse{Skills: out}), nil
}

func (a *AdminServer) CreateSkill(ctx context.Context, req *connect.Request[zarlv1.CreateSkillRequest]) (*connect.Response[zarlv1.CreateSkillResponse], error) {
	if a.skills == nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("skills not configured"))
	}
	sk, err := a.skills.Create(ctx, repository.Skill{
		Name:           req.Msg.Name,
		Description:    req.Msg.Description,
		Markdown:       req.Msg.Markdown,
		ProfileBinding: nonEmpty(req.Msg.ProfileBinding),
		Enabled:        req.Msg.Enabled,
	})
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	a.reloadSkillCache(ctx)
	a.emitConfigChange(fmt.Sprintf("New skill added: %s — %s", sk.Name, sk.Description))
	return connect.NewResponse(&zarlv1.CreateSkillResponse{Skill: skillToProto(sk)}), nil
}

func (a *AdminServer) UpdateSkill(ctx context.Context, req *connect.Request[zarlv1.UpdateSkillRequest]) (*connect.Response[zarlv1.UpdateSkillResponse], error) {
	if a.skills == nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("skills not configured"))
	}
	if err := a.skills.Update(ctx, repository.Skill{
		ID:             req.Msg.Id,
		Name:           req.Msg.Name,
		Description:    req.Msg.Description,
		Markdown:       req.Msg.Markdown,
		ProfileBinding: nonEmpty(req.Msg.ProfileBinding),
		Enabled:        req.Msg.Enabled,
	}); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	a.reloadSkillCache(ctx)
	return connect.NewResponse(&zarlv1.UpdateSkillResponse{}), nil
}

func (a *AdminServer) DeleteSkill(ctx context.Context, req *connect.Request[zarlv1.DeleteSkillRequest]) (*connect.Response[zarlv1.DeleteSkillResponse], error) {
	if a.skills == nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("skills not configured"))
	}
	// Read before delete so we can name what's being removed.
	sk, _ := a.skills.Get(ctx, req.Msg.Id)
	if err := a.skills.Delete(ctx, req.Msg.Id); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	a.reloadSkillCache(ctx)
	if sk.Name != "" {
		a.emitConfigChange(fmt.Sprintf("Skill removed: %s", sk.Name))
	}
	return connect.NewResponse(&zarlv1.DeleteSkillResponse{}), nil
}

func (a *AdminServer) ListSkillProposals(ctx context.Context, req *connect.Request[zarlv1.ListSkillProposalsRequest]) (*connect.Response[zarlv1.ListSkillProposalsResponse], error) {
	if a.skillProposals == nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("skill proposals not configured"))
	}
	rows, err := a.skillProposals.List(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	out := make([]*zarlv1.SkillProposalMsg, len(rows))
	for i, r := range rows {
		out[i] = skillProposalToProto(r)
	}
	return connect.NewResponse(&zarlv1.ListSkillProposalsResponse{Proposals: out}), nil
}

// ReviewSkillProposal approves or rejects a pending proposal. Approval
// applies the proposal to the skills table (create-or-update) in the
// same transaction as the status flip so the cache reload sees the new
// row. Rejection just stamps the status — no skill change.
func (a *AdminServer) ReviewSkillProposal(ctx context.Context, req *connect.Request[zarlv1.ReviewSkillProposalRequest]) (*connect.Response[zarlv1.ReviewSkillProposalResponse], error) {
	if a.skillProposals == nil || a.skills == nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("skill subsystem not configured"))
	}
	p, err := a.skillProposals.Get(ctx, req.Msg.Id)
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, err)
	}
	if p.Status != "pending" {
		return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("proposal already %s", p.Status))
	}
	status := "rejected"
	if req.Msg.Approve {
		status = "approved"
		// Apply the change. Update when target_skill_id is set, else
		// create a new skill. Errors propagate — status is only
		// flipped on successful apply so a failed write leaves the
		// proposal pending for retry.
		if p.TargetSkillID != nil {
			if err := a.skills.Update(ctx, repository.Skill{
				ID:             *p.TargetSkillID,
				Name:           p.ProposedName,
				Description:    p.ProposedDescription,
				Markdown:       p.ProposedMarkdown,
				ProfileBinding: p.ProposedBinding,
				Enabled:        true,
			}); err != nil {
				return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("apply proposal update: %w", err))
			}
		} else {
			// Name is UNIQUE on the skills table — a "new" proposal whose
			// name collides with an existing skill is semantically an
			// update (the LLM re-proposed an existing capability). Detect
			// the collision and switch to Update so the approval path
			// doesn't fail with a constraint violation.
			existing, err := a.skills.List(ctx)
			if err != nil {
				return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("lookup existing skills: %w", err))
			}
			var collision *repository.Skill
			for i := range existing {
				if existing[i].Name == p.ProposedName {
					collision = &existing[i]
					break
				}
			}
			if collision != nil {
				if err := a.skills.Update(ctx, repository.Skill{
					ID:             collision.ID,
					Name:           p.ProposedName,
					Description:    p.ProposedDescription,
					Markdown:       p.ProposedMarkdown,
					ProfileBinding: p.ProposedBinding,
					Enabled:        true,
				}); err != nil {
					return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("apply proposal update (name collision): %w", err))
				}
			} else if _, err := a.skills.Create(ctx, repository.Skill{
				Name:           p.ProposedName,
				Description:    p.ProposedDescription,
				Markdown:       p.ProposedMarkdown,
				ProfileBinding: p.ProposedBinding,
				Enabled:        true,
			}); err != nil {
				return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("apply proposal create: %w", err))
			}
		}
	}
	if err := a.skillProposals.SetStatus(ctx, req.Msg.Id, status); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	a.reloadSkillCache(ctx)
	if status == "approved" {
		a.emitConfigChange(fmt.Sprintf("New skill approved: %s — %s", p.ProposedName, p.ProposedDescription))
	}
	return connect.NewResponse(&zarlv1.ReviewSkillProposalResponse{}), nil
}

// reloadSkillCache re-reads enabled skills from the repo and loads
// them into the in-memory store. Called after every write so the next
// LLM turn picks up the change without a binary restart.
func (a *AdminServer) reloadSkillCache(ctx context.Context) {
	if a.skills == nil || a.skillStore == nil {
		return
	}
	rows, err := a.skills.ListEnabled(ctx)
	if err != nil {
		return
	}
	slim := make([]skills.Skill, len(rows))
	for i, r := range rows {
		binding := ""
		if r.ProfileBinding != nil {
			binding = *r.ProfileBinding
		}
		slim[i] = skills.Skill{
			ID:             r.ID,
			Name:           r.Name,
			Description:    r.Description,
			Markdown:       r.Markdown,
			ProfileBinding: binding,
		}
	}
	a.skillStore.Load(slim)
}

func skillToProto(s repository.Skill) *zarlv1.SkillMsg {
	binding := ""
	if s.ProfileBinding != nil {
		binding = *s.ProfileBinding
	}
	return &zarlv1.SkillMsg{
		Id:             s.ID,
		Name:           s.Name,
		Description:    s.Description,
		Markdown:       s.Markdown,
		ProfileBinding: binding,
		Enabled:        s.Enabled,
		CreatedAt:      s.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
		UpdatedAt:      s.UpdatedAt.Format("2006-01-02T15:04:05Z07:00"),
	}
}

func skillProposalToProto(p repository.SkillProposal) *zarlv1.SkillProposalMsg {
	target := ""
	if p.TargetSkillID != nil {
		target = *p.TargetSkillID
	}
	binding := ""
	if p.ProposedBinding != nil {
		binding = *p.ProposedBinding
	}
	reviewed := ""
	if p.ReviewedAt != nil {
		reviewed = p.ReviewedAt.Format("2006-01-02T15:04:05Z07:00")
	}
	return &zarlv1.SkillProposalMsg{
		Id:                  p.ID,
		TargetSkillId:       target,
		ProposedName:        p.ProposedName,
		ProposedDescription: p.ProposedDescription,
		ProposedMarkdown:    p.ProposedMarkdown,
		ProposedBinding:     binding,
		Rationale:           p.Rationale,
		Status:              p.Status,
		CreatedAt:           p.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
		ReviewedAt:          reviewed,
	}
}

// nonEmpty returns &s when s is non-empty; nil otherwise. Lets callers
// pass a plain string through the proto boundary and have it stored
// as NULL when blank.
func nonEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
