package service

import "errors"

var (
	ErrCanceled       = errors.New("canceled")
	ErrNoFaceDetected = errors.New("no face detected")
	ErrFaceModelsDir  = errors.New("face models directory not found")
)
