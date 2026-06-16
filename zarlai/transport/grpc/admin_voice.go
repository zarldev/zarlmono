package grpc

import (
	"context"
	"fmt"
	"strings"

	"connectrpc.com/connect"
	"github.com/zarldev/zarlmono/zarlai/service"
	zarlv1 "github.com/zarldev/zarlmono/zarlai/transport/grpc/gen/zarl/v1"
)

// Voice Settings — TTS engine + voice configuration + preview.

func (a *AdminServer) GetVoiceSettings(ctx context.Context, req *connect.Request[zarlv1.GetVoiceSettingsRequest]) (*connect.Response[zarlv1.GetVoiceSettingsResponse], error) {
	if a.synthesizer == nil {
		return nil, connect.NewError(connect.CodeUnavailable, fmt.Errorf("TTS not configured"))
	}
	available := a.synthesizer.Engines()
	availableNames := make([]string, len(available))
	for i, e := range available {
		availableNames[i] = string(e)
	}
	return connect.NewResponse(&zarlv1.GetVoiceSettingsResponse{
		Speaker:          int32(a.synthesizer.Speaker()),
		Speed:            a.synthesizer.Speed(),
		NumSpeakers:      int32(a.synthesizer.NumSpeakers()),
		Engine:           string(a.synthesizer.Engine()),
		AvailableEngines: availableNames,
	}), nil
}

func (a *AdminServer) SetVoiceSettings(ctx context.Context, req *connect.Request[zarlv1.SetVoiceSettingsRequest]) (*connect.Response[zarlv1.SetVoiceSettingsResponse], error) {
	if a.synthesizer == nil {
		return nil, connect.NewError(connect.CodeUnavailable, fmt.Errorf("TTS not configured"))
	}

	if req.Msg.Engine != "" {
		if err := a.synthesizer.SwitchEngine(service.EngineName(req.Msg.Engine)); err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, err)
		}
		if err := a.settings.Set(ctx, "voice.engine", req.Msg.Engine); err != nil {
			return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("persist engine: %w", err))
		}
	}

	a.synthesizer.Tune(int(req.Msg.Speaker), req.Msg.Speed)

	// Per-engine voice key — switching back to this engine later restores
	// whichever speaker/speed was last set on it.
	engineKey := "voice." + string(a.synthesizer.Engine())
	v := fmt.Sprintf("%d:%.2f", req.Msg.Speaker, req.Msg.Speed)
	if err := a.settings.Set(ctx, engineKey, v); err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("persist voice setting: %w", err))
	}

	return connect.NewResponse(&zarlv1.SetVoiceSettingsResponse{}), nil
}

func (a *AdminServer) PreviewVoice(ctx context.Context, req *connect.Request[zarlv1.PreviewVoiceRequest]) (*connect.Response[zarlv1.PreviewVoiceResponse], error) {
	if a.synthesizer == nil {
		return nil, connect.NewError(connect.CodeUnavailable, fmt.Errorf("TTS not configured"))
	}
	// Preview temporarily swaps voice; restore on return so the setting is
	// untouched after the preview renders.
	origSpeaker := a.synthesizer.Speaker()
	origSpeed := a.synthesizer.Speed()
	a.synthesizer.Tune(int(req.Msg.Speaker), req.Msg.Speed)
	defer a.synthesizer.Tune(origSpeaker, origSpeed)

	previewText := strings.TrimSpace(req.Msg.Text)
	if previewText == "" {
		previewText = "Hi there! This is what I sound like. How do you like my voice?"
	}
	pcm, err := a.synthesizer.Synthesize(ctx, previewText)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("synthesize preview: %w", err))
	}
	pcmBytes := make([]byte, len(pcm)*2)
	for i, s := range pcm {
		pcmBytes[i*2] = byte(s)
		pcmBytes[i*2+1] = byte(s >> 8)
	}
	return connect.NewResponse(&zarlv1.PreviewVoiceResponse{
		Pcm:        pcmBytes,
		SampleRate: int32(a.synthesizer.SampleRate()),
	}), nil
}
