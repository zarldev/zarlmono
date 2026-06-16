package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"image"
	"image/jpeg"
	"log/slog"
	"strings"
	"sync"

	"github.com/Kagami/go-face"
	"golang.org/x/image/draw"
)

// FaceMatch is what PersonStore.Match returns. Name is empty when the
// closest stored embedding is outside the store's match threshold —
// callers treat that as "unknown speaker". Dist is exposed so the
// service can log it for tuning.
type FaceMatch struct {
	Name  string
	Notes string
	Dist  float64
}

// PersonStore is the slice of person-data access the FaceService
// needs. Defined consumer-side here so the service layer doesn't have
// to import the repository layer (the threshold and Person → FaceMatch
// translation live with the concrete implementation in cmd/zarl/wiring).
type PersonStore interface {
	Match(ctx context.Context, embedding []float32) (FaceMatch, error)
	Enroll(ctx context.Context, name string, embeddings [][]float32, photo string) error
	Forget(ctx context.Context, name string) error
}

// FaceService handles face detection, recognition, enrollment, and forgetting.
type FaceService struct {
	mu      sync.Mutex
	rec     *face.Recognizer
	persons PersonStore
}

// NewFaceService creates a face service with dlib models from modelsDir.
func NewFaceService(modelsDir string, persons PersonStore) (*FaceService, error) {
	if strings.ContainsRune(modelsDir, 0) {
		return nil, fmt.Errorf("init recognizer: modelsDir contains null byte")
	}
	rec, err := face.NewRecognizer(modelsDir)
	if err != nil {
		return nil, fmt.Errorf("init recognizer: %w", err)
	}
	return &FaceService{rec: rec, persons: persons}, nil
}

// Identify detects the largest face in a JPEG and matches it against the store.
// Returns the matched name (empty if unknown), the face embedding, a base64 JPEG photo, and notes.
func (f *FaceService) Identify(jpeg []byte) (string, []float32, string, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	faces, err := f.rec.Recognize(jpeg)
	if err != nil {
		return "", nil, "", "", fmt.Errorf("recognize: %w", err)
	}
	if len(faces) == 0 {
		return "", nil, "", "", ErrNoFaceDetected
	}

	// Use the largest face (by bounding box area)
	best := faces[0]
	bestArea := best.Rectangle.Dx() * best.Rectangle.Dy()
	for _, fc := range faces[1:] {
		area := fc.Rectangle.Dx() * fc.Rectangle.Dy()
		if area > bestArea {
			best = fc
			bestArea = area
		}
	}

	embedding := make([]float32, 128)
	copy(embedding, best.Descriptor[:])

	photo := cropFacePhoto(jpeg, best.Rectangle)

	match, err := f.persons.Match(context.Background(), embedding)
	if err != nil {
		return "", embedding, photo, "", fmt.Errorf("match: %w", err)
	}

	slog.Info("face identify", "match", match.Name, "distance", fmt.Sprintf("%.3f", match.Dist))
	return match.Name, embedding, photo, match.Notes, nil
}

func cropFacePhoto(jpegData []byte, rect image.Rectangle) string {
	img, err := jpeg.Decode(bytes.NewReader(jpegData))
	if err != nil {
		return ""
	}

	// Expand crop by 30% for context
	w := rect.Dx()
	h := rect.Dy()
	padX := w * 3 / 10
	padY := h * 3 / 10
	cropRect := image.Rect(
		max(0, rect.Min.X-padX),
		max(0, rect.Min.Y-padY),
		min(img.Bounds().Max.X, rect.Max.X+padX),
		min(img.Bounds().Max.Y, rect.Max.Y+padY),
	)

	type subImager interface {
		SubImage(r image.Rectangle) image.Image
	}
	si, ok := img.(subImager)
	if !ok {
		return ""
	}
	face := si.SubImage(cropRect)

	// Resize to 256x256
	dst := image.NewRGBA(image.Rect(0, 0, 256, 256))
	draw.BiLinear.Scale(dst, dst.Bounds(), face, face.Bounds(), draw.Over, nil)

	var buf bytes.Buffer
	jpeg.Encode(&buf, dst, &jpeg.Options{Quality: 80})
	return base64.StdEncoding.EncodeToString(buf.Bytes())
}

// Enroll saves a face with the given name. Pass one embedding for the
// legacy single-pose path; pass three for the onboarding wizard flow.
func (f *FaceService) Enroll(name string, embeddings [][]float32, photo string) error {
	if len(embeddings) == 0 {
		return fmt.Errorf("enroll %s: no embeddings", name)
	}
	return f.persons.Enroll(context.Background(), name, embeddings, photo)
}

// Forget removes a face by name.
func (f *FaceService) Forget(name string) error {
	return f.persons.Forget(context.Background(), name)
}

// Close releases the recognizer resources.
func (f *FaceService) Close() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rec.Close()
}
