package modelsdev

//go:generate go tool goenums -f -i modality_enum.go

// modality is the closed subset of catalogue wire modalities understood here.
// Future optional modalities are ignored at the decoding boundary.
type modality int

const (
	modalityUnknown modality = iota // unknown
	modalityText                    // text
	modalityImage                   // image
	modalityAudio                   // audio
	modalityVideo                   // video
	modalityPDF                     // pdf
)
