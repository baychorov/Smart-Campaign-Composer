package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/baychorov/Smart-Campaign-Composer/backend/internal/campaign"
	"github.com/baychorov/Smart-Campaign-Composer/backend/internal/content"
	"github.com/baychorov/Smart-Campaign-Composer/backend/internal/llm"
	"github.com/baychorov/Smart-Campaign-Composer/backend/internal/segment"
)

// campaignView is the JSON shape of a campaign.
type campaignView struct {
	ID           int64              `json:"id"`
	Brief        string             `json:"brief"`
	Status       campaign.Status    `json:"status"`
	Rule         segment.Rule       `json:"rule"`
	Summary      segment.Summary    `json:"summary"`
	Approvable   bool               `json:"approvable"`
	Message      content.Message    `json:"message"`
	Channels     channels           `json:"channels"`
	PartialAckAt *time.Time         `json:"partial_ack_at"`
	ApprovedAt   *time.Time         `json:"approved_at"`
	CreatedAt    time.Time          `json:"created_at"`
	UpdatedAt    time.Time          `json:"updated_at"`
	Audience     *campaign.Audience `json:"audience"` // null when the rule has no audience
}

type channels struct {
	SMS   bool `json:"sms"`
	Email bool `json:"email"`
}

// view builds the JSON view of c, including its audience counts.
func (s *Server) view(ctx context.Context, c campaign.Campaign) (campaignView, error) {
	v := segment.Validate(c.Rule)
	out := campaignView{
		ID:           c.ID,
		Brief:        c.Brief,
		Status:       c.Status,
		Rule:         emptySlices(v.Rule),
		Summary:      segment.Summarize(v.Rule),
		Approvable:   v.Approvable(),
		Message:      c.Message,
		Channels:     channels{SMS: c.SendSMS, Email: c.SendEmail},
		PartialAckAt: c.PartialAckAt,
		ApprovedAt:   c.ApprovedAt,
		CreatedAt:    c.CreatedAt,
		UpdatedAt:    c.UpdatedAt,
	}
	a, ok, err := s.cfg.Campaigns.Audience(ctx, c)
	if err != nil {
		return campaignView{}, err
	}
	if ok {
		out.Audience = &a
	}
	return out, nil
}

// emptySlices turns nil lists into empty ones, so the UI gets [] not null.
func emptySlices(r segment.Rule) segment.Rule {
	r.Common = nonNil(r.Common)
	r.Groups = nonNil(r.Groups)
	r.Assumptions = nonNil(r.Assumptions)
	r.Unsupported = nonNil(r.Unsupported)
	r.Notes = nonNil(r.Notes)
	return r
}

// writeCampaign writes c with its view under "campaign", plus extra fields.
func (s *Server) writeCampaign(w http.ResponseWriter, r *http.Request, status int, c campaign.Campaign, extra map[string]any) {
	v, err := s.view(r.Context(), c)
	if err != nil {
		s.writeCampaignError(w, err)
		return
	}
	body := map[string]any{"campaign": v}
	for k, x := range extra {
		body[k] = x
	}
	writeJSON(w, status, body)
}

// writeCampaignError maps errors of package campaign to responses.
func (s *Server) writeCampaignError(w http.ResponseWriter, err error) {
	var ruleErr *campaign.RuleError
	var approvalErr *campaign.ApprovalError
	switch {
	case errors.Is(err, campaign.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "Kampanya bulunamadı.")
	case errors.Is(err, campaign.ErrNotDraft):
		writeError(w, http.StatusConflict, "not_draft", "Kampanya artık taslak değil; değiştirilemez veya tekrar onaylanamaz.")
	case errors.Is(err, campaign.ErrNotApprovable):
		writeError(w, http.StatusUnprocessableEntity, "rule_not_approvable", "Kural bir kitle tanımlamıyor.")
	case errors.As(err, &ruleErr):
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
			"error": "invalid_rule", "message": "Kural geçerli değil.", "issues": nonNil(ruleErr.Issues),
		})
	case errors.As(err, &approvalErr):
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
			"error": "approval_blocked", "message": "Kampanya onaylanamadı.", "issues": nonNil(approvalErr.Issues),
		})
	default:
		s.cfg.Log.Error("campaign request failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal", "Beklenmeyen bir hata oluştu.")
	}
}

// campaignID parses the {id} path value. An invalid id is a 404: no
// campaign has it.
func campaignID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusNotFound, "not_found", "Kampanya bulunamadı.")
		return 0, false
	}
	return id, true
}

// createCampaign: POST /api/campaigns {brief}. Runs segment extraction
// synchronously and stores the draft.
func (s *Server) createCampaign(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Brief string `json:"brief"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	start := s.cfg.Now()
	c, res, err := s.cfg.Campaigns.Create(r.Context(), req.Brief)

	// The brief and the rule are not logged: they may contain personal data.
	usage := res.Usage()
	attrs := []any{"repairs", res.Repairs, "attempts", len(res.Attempts),
		"input_tokens", usage.InputTokens, "output_tokens", usage.OutputTokens, "duration", s.cfg.Now().Sub(start)}

	switch {
	case err == nil:
		s.cfg.Log.Info("segment extraction finished", append(attrs, "campaign_id", c.ID, "status", c.Rule.Status)...)
		s.writeCampaign(w, r, http.StatusCreated, c, map[string]any{"warnings": nonNil(res.Validation.Warnings)})
	case errors.Is(err, llm.ErrInvalidBrief):
		writeError(w, http.StatusBadRequest, "invalid_brief", "Brief boş olamaz ve en fazla "+strconv.Itoa(llm.MaxBriefRunes)+" karakter olabilir.")
	case errors.Is(err, llm.ErrInvalidOutput):
		s.cfg.Log.Warn("segment extraction invalid", attrs...)
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
			"error":   "invalid_rule",
			"message": "Model geçerli bir kural üretemedi.",
			"issues":  nonNil(res.Issues),
		})
	default:
		code, msg := failure(err)
		s.cfg.Log.Warn("segment extraction failed", append(attrs, "code", code, "err", err)...)
		status := http.StatusBadGateway
		switch code {
		case "refusal":
			status = http.StatusUnprocessableEntity
		case "timeout":
			status = http.StatusGatewayTimeout
		}
		writeError(w, status, code, msg)
	}
}

func (s *Server) getCampaign(w http.ResponseWriter, r *http.Request) {
	id, ok := campaignID(w, r)
	if !ok {
		return
	}
	c, err := s.cfg.Campaigns.Get(r.Context(), id)
	if err != nil {
		s.writeCampaignError(w, err)
		return
	}
	s.writeCampaign(w, r, http.StatusOK, c, nil)
}

// updateRule: PUT /api/campaigns/{id}/rule {rule}.
func (s *Server) updateRule(w http.ResponseWriter, r *http.Request) {
	id, ok := campaignID(w, r)
	if !ok {
		return
	}
	var req struct {
		Rule segment.Rule `json:"rule"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	c, v, err := s.cfg.Campaigns.UpdateRule(r.Context(), id, req.Rule)
	if err != nil {
		s.writeCampaignError(w, err)
		return
	}
	s.writeCampaign(w, r, http.StatusOK, c, map[string]any{"warnings": nonNil(v.Warnings)})
}

// saveContent: PUT /api/campaigns/{id}/content {sms, email_subject,
// email_body}. The message is stored even when invalid (a draft); the
// response carries its validation (D-CONTENT).
func (s *Server) saveContent(w http.ResponseWriter, r *http.Request) {
	id, ok := campaignID(w, r)
	if !ok {
		return
	}
	var m content.Message
	if !decodeBody(w, r, &m) {
		return
	}
	c, res, err := s.cfg.Campaigns.SaveContent(r.Context(), id, m)
	if err != nil {
		s.writeCampaignError(w, err)
		return
	}
	s.writeCampaign(w, r, http.StatusOK, c, map[string]any{"validation": map[string]any{
		"valid":  res.Valid(),
		"errors": nonNil(res.Errors),
		"stats":  res.Stats,
	}})
}

// startGeneration: POST /api/campaigns/{id}/content/generations. The brief
// and rule come from the stored draft, not from the client.
func (s *Server) startGeneration(w http.ResponseWriter, r *http.Request) {
	id, ok := campaignID(w, r)
	if !ok {
		return
	}
	in, longest, err := s.cfg.Campaigns.GenerationInput(r.Context(), id)
	if err != nil {
		s.writeCampaignError(w, err)
		return
	}
	genID := s.launchGeneration(in, longest)
	writeJSON(w, http.StatusAccepted, map[string]string{
		"id":         genID,
		"events_url": "/api/content/generations/" + genID + "/events",
	})
}

// approveCampaign: POST /api/campaigns/{id}/approve
// {channels: {sms, email}, partial_ack}.
func (s *Server) approveCampaign(w http.ResponseWriter, r *http.Request) {
	id, ok := campaignID(w, r)
	if !ok {
		return
	}
	var req struct {
		Channels   channels `json:"channels"`
		PartialAck bool     `json:"partial_ack"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	c, snap, err := s.cfg.Campaigns.Approve(r.Context(), id, campaign.ApproveRequest{
		SMS: req.Channels.SMS, Email: req.Channels.Email, PartialAck: req.PartialAck,
	})
	if err != nil {
		s.writeCampaignError(w, err)
		return
	}
	s.cfg.Log.Info("campaign approved", "campaign_id", c.ID, "sms", snap.SMS, "email", snap.Email)
	s.writeCampaign(w, r, http.StatusOK, c, map[string]any{"snapshot": snap})
}
