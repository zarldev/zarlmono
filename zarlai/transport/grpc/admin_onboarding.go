package grpc

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/zarldev/zarlmono/zarlai/repository"
	"github.com/zarldev/zarlmono/zarlai/service"
	"github.com/zarldev/zarlmono/zarlai/taskrunner"
	"github.com/zarldev/zarlmono/zarlai/tools/memory"
	zarlv1 "github.com/zarldev/zarlmono/zarlai/transport/grpc/gen/zarl/v1"
	"github.com/zarldev/zarlmono/zkit/vectorstore/qdrant"
)

// NOTE: form-field templating happens in the wizard frontend before this
// call. The typed fields on CompleteOnboardingRequest (PersonDob, Family,
// …) are forward-compat scaffolding for a future server-side templater
// or admin re-render — today only FreeFormFacts is what gets stored in
// memory. Keep them populated in the request anyway so the data is on
// the wire if we ever need it.

// CompleteOnboarding is the wizard's atomic Finish. It enrols the person
// with multi-pose embeddings, writes the agent name + voice + model
// settings, and seeds memory bullets for each form field. Hard-fails on
// person/settings writes; best-effort per memory bullet (logs + continues
// so a single embedding glitch doesn't stall the wizard).
func (a *AdminServer) CompleteOnboarding(ctx context.Context, req *connect.Request[zarlv1.CompleteOnboardingRequest]) (*connect.Response[zarlv1.CompleteOnboardingResponse], error) {
	if a.persons == nil || a.settings == nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("admin not fully configured"))
	}
	m := req.Msg

	person, err := a.enrolPersonFromOnboarding(ctx, m)
	if err != nil {
		return nil, err
	}
	if err := a.applyAgentNameFromOnboarding(ctx, m); err != nil {
		return nil, err
	}
	if err := a.applyVoiceFromOnboarding(ctx, m); err != nil {
		return nil, err
	}
	if err := a.applyLLMModelFromOnboarding(ctx, m); err != nil {
		return nil, err
	}
	memoriesWritten := a.seedMemoriesFromOnboarding(ctx, m)

	a.emitConfigChange(fmt.Sprintf("Onboarding complete: %s enrolled with %d poses, %d memories.",
		m.PersonName, len(m.FaceEmbeddings), memoriesWritten))

	return connect.NewResponse(&zarlv1.CompleteOnboardingResponse{
		PersonId:        string(person.ID),
		MemoriesWritten: memoriesWritten,
	}), nil
}

// enrolPersonFromOnboarding validates the multi-pose embeddings (each
// must be 128 floats) and creates the person row. Hard-fails — without
// at least one valid embedding face recognition can't identify the
// user, and the rest of the wizard has nothing to attach memories to.
func (a *AdminServer) enrolPersonFromOnboarding(ctx context.Context, m *zarlv1.CompleteOnboardingRequest) (repository.Person, error) {
	if len(m.FaceEmbeddings) == 0 {
		return repository.Person{}, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("at least one face embedding required"))
	}
	embs := make([][]float32, 0, len(m.FaceEmbeddings))
	for _, e := range m.FaceEmbeddings {
		if len(e.Values) != 128 {
			return repository.Person{}, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("each face embedding must be 128 floats"))
		}
		embs = append(embs, e.Values)
	}
	person, err := a.persons.Create(ctx, m.PersonName, embs, m.FacePhotoJpegBase64)
	if err != nil {
		return repository.Person{}, connect.NewError(connect.CodeInternal, fmt.Errorf("enrol person: %w", err))
	}
	return person, nil
}

// applyAgentNameFromOnboarding persists the agent name (defaulting if
// blank), reconfigures live ZarlServer + Runner so the next turn
// reflects it, and clears any stale spoken-name override so TTS uses
// the new display name.
func (a *AdminServer) applyAgentNameFromOnboarding(ctx context.Context, m *zarlv1.CompleteOnboardingRequest) error {
	agentName := strings.TrimSpace(m.AgentName)
	if agentName == "" {
		agentName = service.DefaultAgentName
	}
	if err := a.settings.Set(ctx, agentNameSettingKey, agentName); err != nil {
		return connect.NewError(connect.CodeInternal, fmt.Errorf("persist agent name: %w", err))
	}
	if a.zarlServer != nil {
		a.zarlServer.Reconfigure(WithAgentName(agentName))
	}
	if a.runner != nil {
		a.runner.Reconfigure(taskrunner.WithAgentName(agentName))
	}
	if err := a.settings.Set(ctx, agentSpokenNameSettingKey, ""); err != nil {
		return connect.NewError(connect.CodeInternal, fmt.Errorf("clear agent spoken name: %w", err))
	}
	return nil
}

// applyVoiceFromOnboarding tunes the synthesizer in place and persists
// the speaker:speed pair so the setting survives restart.
func (a *AdminServer) applyVoiceFromOnboarding(ctx context.Context, m *zarlv1.CompleteOnboardingRequest) error {
	if a.synthesizer != nil {
		a.synthesizer.Tune(int(m.VoiceSpeaker), m.VoiceSpeed)
	}
	if err := a.settings.Set(ctx, "voice", fmt.Sprintf("%d:%.2f", m.VoiceSpeaker, m.VoiceSpeed)); err != nil {
		return connect.NewError(connect.CodeInternal, fmt.Errorf("persist voice: %w", err))
	}
	return nil
}

// applyLLMModelFromOnboarding writes the model setting and rebuilds
// the live conversation LLM with it. Reconfigure failure logs but
// doesn't block — the model setting is saved either way; an admin-side
// fix later can re-call buildLLM.
func (a *AdminServer) applyLLMModelFromOnboarding(ctx context.Context, m *zarlv1.CompleteOnboardingRequest) error {
	model := strings.TrimSpace(m.LlmModel)
	if model == "" {
		return nil
	}
	if err := a.settings.Set(ctx, "llm_model", model); err != nil {
		return connect.NewError(connect.CodeInternal, fmt.Errorf("persist llm_model: %w", err))
	}
	if a.zarlServer == nil {
		return nil
	}
	provider, _ := a.settings.Get(ctx, "llm_provider")
	if provider == "" {
		provider = "ollama"
	}
	baseURL, _ := a.settings.Get(ctx, "llm_base_url")
	apiKey, _ := a.settings.Get(ctx, "llm_api_key")
	reasoning, _ := a.settings.Get(ctx, "llm_reasoning")
	llm, err := a.buildLLM(LLMSpec{
		Provider:  provider,
		BaseURL:   baseURL,
		APIKey:    apiKey,
		Model:     model,
		Reasoning: reasoning == "true",
	})
	if err != nil {
		slog.Warn("onboarding: reconfigure llm failed (model setting still saved)", "err", err)
		return nil
	}
	a.zarlServer.Reconfigure(WithLLM(llm))
	return nil
}

// seedMemoriesFromOnboarding writes each free-form fact verbatim into
// the per-person memory store. Best-effort: a single embedding or
// upsert glitch logs and continues so the wizard still completes.
// Returns the count actually persisted.
func (a *AdminServer) seedMemoriesFromOnboarding(ctx context.Context, m *zarlv1.CompleteOnboardingRequest) int64 {
	if a.qdrantClient == nil || a.embedder == nil {
		return 0
	}
	var written int64
	for _, fact := range m.FreeFormFacts {
		fact = strings.TrimSpace(fact)
		if fact == "" {
			continue
		}
		vec, err := a.embedder.Embed(ctx, fact)
		if err != nil {
			slog.Warn("onboarding: embed fact failed", "fact", fact, "err", err)
			continue
		}
		point := qdrant.Point{
			ID:     uuid.New().String(),
			Vector: vec,
			Payload: map[string]any{
				"person_name": m.PersonName,
				"fact":        fact,
				"created_at":  time.Now().UTC().Format(time.RFC3339),
			},
		}
		if err := a.qdrantClient.Upsert(ctx, memory.Collection, []qdrant.Point{point}); err != nil {
			slog.Warn("onboarding: upsert fact failed", "fact", fact, "err", err)
			continue
		}
		written++
	}
	return written
}

// EmbedFace runs face detection + 128-dim embedding on a JPEG and returns
// the descriptor + cropped photo. Used by the onboarding wizard to capture
// each pose without going through the conversation pipeline.
func (a *AdminServer) EmbedFace(ctx context.Context, req *connect.Request[zarlv1.EmbedFaceRequest]) (*connect.Response[zarlv1.EmbedFaceResponse], error) {
	if a.zarlServer == nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("face service not configured"))
	}
	face := a.zarlServer.Face()
	if face == nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("face service not configured"))
	}
	_, embedding, photo, _, err := face.Identify(req.Msg.Jpeg)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("identify: %w", err))
	}
	if len(embedding) != 128 {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("unexpected embedding length %d", len(embedding)))
	}
	return connect.NewResponse(&zarlv1.EmbedFaceResponse{
		Embedding:       &zarlv1.FaceEmbedding{Values: embedding},
		PhotoJpegBase64: photo,
	}), nil
}
