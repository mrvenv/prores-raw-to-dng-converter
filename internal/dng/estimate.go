package dng

// Typical stored size per sensel, in bytes, measured on a Panasonic GH7 clip
// (5728x3024, ISO 500; docs/COMPRESSION.md). The lossless modes depend on
// the footage: noisier clips store more.
const (
	uncompressedBytesPerSensel = bytesPerSample
	losslessBytesPerSensel     = 1.04
	twelveBitBytesPerSensel    = 0.8
	// rawBytesPerSensel is uncompressed 12-bit raw, the size ratios are
	// quoted against.
	rawBytesPerSensel = 1.5
)

// EstimatedFrameBytes is about how large one width x height frame is when
// stored with c. A ratio mode is estimated at its target ratio; the curve
// modes (3:1, 5:1, 8:1) can stop short of it on clean footage and come out
// larger.
func (c Compression) EstimatedFrameBytes(width, height int) int64 {
	sensels := float64(width) * float64(height)
	switch {
	case c == Uncompressed:
		return int64(sensels * uncompressedBytesPerSensel)
	case c == LosslessJPEG:
		return int64(sensels * losslessBytesPerSensel)
	case c == LosslessJPEG12:
		return int64(sensels * twelveBitBytesPerSensel)
	case c.TargetRatio() > 0:
		return int64(sensels * rawBytesPerSensel / c.TargetRatio())
	default:
		return 0
	}
}
