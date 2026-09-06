package computer_test

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"image"
	"image/png"
	"strings"
	"testing"

	model "github.com/zarldev/zarlmono/zkit/agent/computer"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
	toolcomputer "github.com/zarldev/zarlmono/zkit/ai/tools/computer"
)

func TestObserveToolScreenshotParts(t *testing.T) {
	t.Parallel()
	backendErr := errors.New("capture interrupted")
	const maxScreenshotBytes = 4 * 1024 * 1024
	const pngPrefix = "data:image/png;base64,"
	pngBytes := tinyPNG(t)
	pngPayload := base64.StdEncoding.EncodeToString(pngBytes)
	pngDataURI := pngPrefix + pngPayload
	maxPayloadBytes := (maxScreenshotBytes - len(pngPrefix)) / 4 * 4
	boundaryDecodedBytes := maxPayloadBytes / 4 * 3
	boundaryBytes := append([]byte(nil), pngBytes...)
	boundaryBytes = append(boundaryBytes, make([]byte, boundaryDecodedBytes-len(boundaryBytes))...)
	boundaryDataURI := pngPrefix + base64.StdEncoding.EncodeToString(boundaryBytes)
	if len(boundaryDataURI) > maxScreenshotBytes {
		t.Fatalf("boundary fixture is %d bytes, max %d", len(boundaryDataURI), maxScreenshotBytes)
	}
	for _, tc := range []struct {
		name  string
		image *model.ObservationImage
		err   error
		kind  tools.Kind
	}{
		{name: "metadata only"},
		{name: "image", image: &model.ObservationImage{MIMEType: "image/png", DataURI: pngDataURI}},
		{name: "encoded size boundary", image: &model.ObservationImage{MIMEType: "image/png", DataURI: boundaryDataURI}},
		{name: "oversized", image: &model.ObservationImage{MIMEType: "image/png", DataURI: boundaryDataURI + "AAAA"}, kind: tools.Kinds.BUDGET},
		{name: "non-image bytes", image: &model.ObservationImage{MIMEType: "image/png", DataURI: pngPrefix + base64.StdEncoding.EncodeToString([]byte("not an image"))}, kind: tools.Kinds.FATAL},
		{name: "invalid base64", image: &model.ObservationImage{MIMEType: "image/png", DataURI: pngPrefix + "%%%"}, kind: tools.Kinds.FATAL},
		{name: "bytes mismatch declared MIME", image: &model.ObservationImage{MIMEType: "image/jpeg", DataURI: "data:image/jpeg;base64," + pngPayload}, kind: tools.Kinds.FATAL},
		{name: "unsupported MIME type", image: &model.ObservationImage{MIMEType: "image/webp", DataURI: "data:image/webp;base64," + pngPayload}, kind: tools.Kinds.FATAL},
		{name: "backend error", err: backendErr, kind: tools.Kinds.FATAL},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			obs := model.Observation{
				Surface:     model.SurfaceInfo{Kind: model.SurfaceKinds.BROWSER, Title: "screen"},
				VisibleText: "visible text", Screenshot: tc.image,
			}
			tool := toolcomputer.NewObserveTool(fakeObserver{obs: obs, err: tc.err})
			result, err := tool.Execute(t.Context(), tools.ToolCall{ID: "observe-1", Arguments: tools.ToolParameters{"include_screenshot": tc.image != nil}})
			if err != nil {
				t.Fatalf("Execute: %v", err)
			}
			if result.ToolCallID != "observe-1" {
				t.Fatalf("call ID = %q", result.ToolCallID)
			}
			if tc.kind != tools.Kinds.UNKNOWN {
				if result.Success || result.Err == nil || result.Err.Kind != tc.kind || len(result.Parts) != 0 {
					t.Fatalf("expected %s failure without attachments", tc.kind)
				}
				if tc.err != nil && !errors.Is(result.Err, tc.err) {
					t.Fatalf("error = %v, want %v", result.Err, tc.err)
				}
				return
			}
			if !result.Success {
				t.Fatalf("Execute: %s", result.Error)
			}
			metadata, ok := tools.DataAs[model.Observation](result)
			if !ok || metadata.VisibleText != obs.VisibleText || metadata.Surface != obs.Surface {
				t.Fatal("observation metadata lost")
			}
			if tc.image == nil {
				if len(result.Parts) != 0 || metadata.Screenshot != nil {
					t.Fatal("unexpected screenshot")
				}
				return
			}
			if len(result.Parts) != 1 || result.Parts[0].Type != llm.ContentTypeImage || result.Parts[0].Image == nil {
				t.Fatal("missing image content part")
			}
			if got := result.Parts[0].Image; got.DataURI != tc.image.DataURI || got.MIMEType != tc.image.MIMEType {
				t.Fatal("image payload changed")
			}
			if metadata.Screenshot == nil || metadata.Screenshot.DataURI != "" || metadata.Screenshot.MIMEType != tc.image.MIMEType {
				t.Fatal("screenshot metadata not separated from bytes")
			}
			raw, err := json.Marshal(result.Data)
			if err != nil {
				t.Fatalf("marshal metadata: %v", err)
			}
			if strings.Contains(string(raw), tc.image.DataURI) {
				t.Fatal("image bytes duplicated in text metadata")
			}
		})
	}
}

func tinyPNG(t *testing.T) []byte {
	t.Helper()
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, image.NewRGBA(image.Rect(0, 0, 1, 1))); err != nil {
		t.Fatalf("encode PNG fixture: %v", err)
	}
	return encoded.Bytes()
}
