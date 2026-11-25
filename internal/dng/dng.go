// Package dng writes single-frame CinemaDNG files (little-endian TIFF/DNG)
// containing an RGGB Bayer CFA image: uncompressed, lossless-JPEG tiled,
// or, for DaVinci Resolve only, DCT-JPEG tiled (dct.go).
package dng

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"sort"
	"strings"
	"time"
)

// Compression selects how the CFA image is stored.
type Compression int

const (
	// Uncompressed stores 16-bit samples in one strip.
	Uncompressed Compression = iota
	// LosslessJPEG stores 16-bit samples as lossless JPEG tiles (bit-exact).
	LosslessJPEG
	// LosslessJPEG12 maps samples to 12 bits through a square-root curve
	// (inverted by LinearizationTable) and stores them as lossless JPEG
	// tiles. Quantisation stays below photon shot noise: visually lossless.
	LosslessJPEG12
	// Ratio3, Ratio5 and Ratio8 coarsen the square-root curve until lossless
	// JPEG reaches about 3:1, 5:1 or 8:1 against uncompressed 12-bit raw.
	// The curve is chosen once per clip (CalibrateLevels, Frame.CurveLevels).
	Ratio3
	Ratio5
	Ratio8
	// ResolveRatio3 to ResolveRatio16 store the 12-bit curve as lossy DCT
	// JPEG tiles (dct.go), scaled per clip to about 3:1 ... 16:1 against
	// uncompressed 12-bit raw (CalibrateQuant, Frame.QuantScale). Far less
	// visible loss than the curve ratio modes, but DNG allows DCT only for
	// demosaiced images: DaVinci Resolve reads these frames, macOS and
	// Adobe show them black.
	ResolveRatio3
	ResolveRatio5
	ResolveRatio8
	ResolveRatio12
	ResolveRatio16
)

// Compressions lists every mode, in the order the UI offers them.
var Compressions = []Compression{
	Uncompressed, LosslessJPEG, LosslessJPEG12, Ratio3, Ratio5, Ratio8,
	ResolveRatio3, ResolveRatio5, ResolveRatio8, ResolveRatio12, ResolveRatio16,
}

var compressionNames = map[Compression]string{
	Uncompressed:   "none",
	LosslessJPEG:   "lossless",
	LosslessJPEG12: "12bit",
	Ratio3:         "3:1",
	Ratio5:         "5:1",
	Ratio8:         "8:1",
	ResolveRatio3:  "resolve-3:1",
	ResolveRatio5:  "resolve-5:1",
	ResolveRatio8:  "resolve-8:1",
	ResolveRatio12: "resolve-12:1",
	ResolveRatio16: "resolve-16:1",
}

var compressionLabels = map[Compression]string{
	Uncompressed:   "Uncompressed (16-bit)",
	LosslessJPEG:   "Lossless JPEG (16-bit)",
	LosslessJPEG12: "JPEG 12-bit (visually lossless)",
	Ratio3:         "JPEG 3:1",
	Ratio5:         "JPEG 5:1",
	Ratio8:         "JPEG 8:1",
	ResolveRatio3:  "Resolve only: DCT 3:1",
	ResolveRatio5:  "Resolve only: DCT 5:1",
	ResolveRatio8:  "Resolve only: DCT 8:1",
	ResolveRatio12: "Resolve only: DCT 12:1",
	ResolveRatio16: "Resolve only: DCT 16:1",
}

var targetRatios = map[Compression]float64{
	Ratio3: 3, Ratio5: 5, Ratio8: 8,
	ResolveRatio3: 3, ResolveRatio5: 5, ResolveRatio8: 8, ResolveRatio12: 12, ResolveRatio16: 16,
}

// TargetRatio is the compression ratio the mode aims for, 0 if it has none.
func (c Compression) TargetRatio() float64 { return targetRatios[c] }

// IsDCT reports whether the mode stores lossy DCT tiles (Resolve only).
func (c Compression) IsDCT() bool { return c >= ResolveRatio3 && c <= ResolveRatio16 }

// String returns the short name used on the command line.
func (c Compression) String() string { return compressionNames[c] }

// Label returns the human-readable name shown in the GUI.
func (c Compression) Label() string { return compressionLabels[c] }

// ParseCompression parses a name returned by String.
func ParseCompression(s string) (Compression, error) {
	for _, c := range Compressions {
		if c.String() == s {
			return c, nil
		}
	}
	names := make([]string, len(Compressions))
	for i, c := range Compressions {
		names[i] = c.String()
	}
	return 0, fmt.Errorf("unknown compression %q (want one of %s)", s, strings.Join(names, ", "))
}

// Frame describes one CinemaDNG frame.
type Frame struct {
	Width, Height int
	// Data holds Width*Height little-endian uint16 samples, RGGB pattern.
	Data        []byte
	Compression Compression
	// CurveLevels is the number of curve codes for Ratio3/5/8, normally
	// from CalibrateLevels on the clip's first frame.
	CurveLevels int
	// QuantScale scales the DCT quantisation table of the Resolve modes,
	// normally from CalibrateQuant on the clip's first frame.
	QuantScale float64

	Make, Model, UniqueCameraModel, Software string

	BlackLevel, WhiteLevel int
	ColorMatrix1           [9]float64 // XYZ -> camera, row-major
	AsShotNeutral          [3]float64
	BaselineExposure       float64

	// Default crop in pixels. Zero CropW/CropH means full frame.
	CropX, CropY, CropW, CropH int

	TimeCode   *[8]byte // SMPTE 12M BCD, nil if absent
	FrameRateN int
	FrameRateD int

	// Description goes to ImageDescription, e.g. the recorder used.
	Description string
	// DateTime is when the frame was shot; zero omits the date tags.
	DateTime time.Time
	Exif     Exif
}

// Exif holds shooting parameters for the EXIF sub-IFD; zero fields are omitted.
type Exif struct {
	ISO          int
	FNumber      float64
	ExposureTime float64 // seconds
}

// TIFF 6.0 / DNG 1.4 tag IDs.
const (
	tagNewSubfileType         = 254
	tagImageWidth             = 256
	tagImageLength            = 257
	tagBitsPerSample          = 258
	tagCompression            = 259
	tagPhotometric            = 262
	tagMake                   = 271
	tagModel                  = 272
	tagStripOffsets           = 273
	tagOrientation            = 274
	tagSamplesPerPixel        = 277
	tagRowsPerStrip           = 278
	tagStripByteCounts        = 279
	tagPlanarConfig           = 284
	tagSoftware               = 305
	tagTileWidth              = 322
	tagTileLength             = 323
	tagTileOffsets            = 324
	tagTileByteCounts         = 325
	tagCFARepeatPatternDim    = 33421
	tagCFAPattern             = 33422
	tagDNGVersion             = 50706
	tagDNGBackwardVersion     = 50707
	tagUniqueCameraModel      = 50708
	tagLinearizationTable     = 50712
	tagBlackLevelRepeatDim    = 50713
	tagBlackLevel             = 50714
	tagWhiteLevel             = 50717
	tagDefaultCropOrigin      = 50719
	tagDefaultCropSize        = 50720
	tagColorMatrix1           = 50721
	tagAsShotNeutral          = 50728
	tagBaselineExposure       = 50730
	tagCalibrationIlluminant1 = 50778
	tagTimeCodes              = 51043 // CinemaDNG
	tagFrameRate              = 51044 // CinemaDNG
	tagImageDescription       = 270
	tagDateTime               = 306
	tagExifIFD                = 34665
)

// EXIF 2.3 tag IDs (written in the EXIF sub-IFD).
const (
	exifExposureTime     = 33434
	exifFNumber          = 33437
	exifISO              = 34855
	exifVersion          = 36864
	exifDateTimeOriginal = 36867
)

const (
	// exifDateLayout is the EXIF/TIFF date format "YYYY:MM:DD HH:MM:SS".
	exifDateLayout = "2006:01:02 15:04:05"
	maxShortValue  = math.MaxUint16
)

var exifVersion230 = []byte("0230")

// TIFF field types.
const (
	typeByte      = 1
	typeASCII     = 2
	typeShort     = 3
	typeLong      = 4
	typeRational  = 5
	typeUndefined = 7
	typeSRational = 10
)

// Tag values.
const (
	subfileMainImage     = 0
	compressionNone      = 1
	compressionJPEG      = 7 // DNG spec: JPEG (lossless; Resolve also reads DCT, see dct.go)
	photometricCFA       = 32803
	orientationTopLeft   = 1
	planarChunky         = 1
	illuminantD65        = 21 // EXIF LightSource
	bitsUncompressed     = 16
	defaultUniqueModel   = "ProRes RAW Camera"
	cfaRepeat            = 2 // 2x2 Bayer
	rationalDenominator  = 1000000
	tiffHeaderSize       = 8
	ifdEntrySize         = 12
	inlineValueSize      = 4
	bytesPerSample       = 2
	writeBufferSize      = 1 << 20
	tiffMagic            = 42
	tiffLittleEndianMark = "II"
)

var (
	cfaPatternRGGB = []byte{0, 1, 1, 2} // 0 = red, 1 = green, 2 = blue
	dngVersion     = []byte{1, 4, 0, 0}
	dngBackwardVer = []byte{1, 3, 0, 0}
	le             = binary.LittleEndian
)

type entry struct {
	tag, typ uint16
	count    uint32
	data     []byte
}

// Write encodes f as a DNG.
func Write(w io.Writer, f *Frame) error {
	if n := f.Width * f.Height * bytesPerSample; len(f.Data) != n {
		return fmt.Errorf("dng: data is %d bytes, want %d", len(f.Data), n)
	}
	img, err := encodeImage(f)
	if err != nil {
		return err
	}
	layout := tiffLayout{
		ifd0:       append(commonEntries(f), img.entries...),
		exif:       exifEntries(f),
		offsetsTag: img.offsetsTag,
		chunks:     img.chunks,
	}
	if len(layout.exif) > 0 {
		layout.ifd0 = append(layout.ifd0, entry{tagExifIFD, typeLong, 1, longs(0)})
	}
	return writeTIFF(w, layout)
}

// exifEntries returns the EXIF sub-IFD entries, or none if f has no
// shooting parameters.
func exifEntries(f *Frame) []entry {
	var e []entry
	if f.Exif.ExposureTime > 0 {
		e = append(e, entry{exifExposureTime, typeRational, 1, urats(f.Exif.ExposureTime)})
	}
	if f.Exif.FNumber > 0 {
		e = append(e, entry{exifFNumber, typeRational, 1, urats(f.Exif.FNumber)})
	}
	if f.Exif.ISO > 0 {
		e = append(e, entry{exifISO, typeShort, 1, shorts(min(f.Exif.ISO, maxShortValue))})
	}
	if !f.DateTime.IsZero() {
		e = append(e, asciiEntry(exifDateTimeOriginal, f.DateTime.Format(exifDateLayout)))
	}
	if len(e) > 0 {
		e = append(e, entry{exifVersion, typeUndefined, uint32(len(exifVersion230)), exifVersion230})
	}
	return e
}

func asciiEntry(tag uint16, s string) entry {
	d := append([]byte(s), 0)
	return entry{tag, typeASCII, uint32(len(d)), d}
}

// encodedImage is the stored image: its chunks (strips or tiles) and the
// tags describing them. The offsets tag is filled in once layout is known.
type encodedImage struct {
	entries    []entry
	offsetsTag uint16
	chunks     [][]byte
}

func encodeImage(f *Frame) (encodedImage, error) {
	switch f.Compression {
	case Uncompressed:
		return encodedImage{
			entries: []entry{
				{tagBitsPerSample, typeShort, 1, shorts(bitsUncompressed)},
				{tagCompression, typeShort, 1, shorts(compressionNone)},
				{tagStripOffsets, typeLong, 1, longs(0)},
				{tagRowsPerStrip, typeLong, 1, longs(f.Height)},
				{tagStripByteCounts, typeLong, 1, longs(len(f.Data))},
			},
			offsetsTag: tagStripOffsets,
			chunks:     [][]byte{f.Data},
		}, nil
	case LosslessJPEG, LosslessJPEG12, Ratio3, Ratio5, Ratio8:
		return encodeTiled(f)
	case ResolveRatio3, ResolveRatio5, ResolveRatio8, ResolveRatio12, ResolveRatio16:
		return encodeDCT(f)
	default:
		return encodedImage{}, fmt.Errorf("dng: unknown compression %d", f.Compression)
	}
}

func commonEntries(f *Frame) []entry {
	cropW, cropH := f.CropW, f.CropH
	if cropW <= 0 || cropH <= 0 {
		cropW, cropH = f.Width, f.Height
	}
	e := []entry{
		{tagNewSubfileType, typeLong, 1, longs(subfileMainImage)},
		{tagImageWidth, typeLong, 1, longs(f.Width)},
		{tagImageLength, typeLong, 1, longs(f.Height)},
		{tagPhotometric, typeShort, 1, shorts(photometricCFA)},
		{tagOrientation, typeShort, 1, shorts(orientationTopLeft)},
		{tagSamplesPerPixel, typeShort, 1, shorts(1)},
		{tagPlanarConfig, typeShort, 1, shorts(planarChunky)},
		{tagCFARepeatPatternDim, typeShort, 2, shorts(cfaRepeat, cfaRepeat)},
		{tagCFAPattern, typeByte, uint32(len(cfaPatternRGGB)), cfaPatternRGGB},
		{tagDNGVersion, typeByte, uint32(len(dngVersion)), dngVersion},
		{tagDNGBackwardVersion, typeByte, uint32(len(dngBackwardVer)), dngBackwardVer},
		{tagBlackLevelRepeatDim, typeShort, 2, shorts(1, 1)},
		{tagBlackLevel, typeLong, 1, longs(f.BlackLevel)},
		{tagWhiteLevel, typeLong, 1, longs(f.WhiteLevel)},
		{tagDefaultCropOrigin, typeLong, 2, longs(f.CropX, f.CropY)},
		{tagDefaultCropSize, typeLong, 2, longs(cropW, cropH)},
		{tagColorMatrix1, typeSRational, 9, srats(f.ColorMatrix1[:]...)},
		{tagAsShotNeutral, typeRational, 3, urats(f.AsShotNeutral[:]...)},
		{tagBaselineExposure, typeSRational, 1, srats(f.BaselineExposure)},
		{tagCalibrationIlluminant1, typeShort, 1, shorts(illuminantD65)},
	}
	ucm := f.UniqueCameraModel
	if ucm == "" {
		ucm = defaultUniqueModel
	}
	for _, s := range []struct {
		tag uint16
		val string
	}{
		{tagMake, f.Make}, {tagModel, f.Model}, {tagSoftware, f.Software},
		{tagUniqueCameraModel, ucm}, {tagImageDescription, f.Description},
	} {
		if s.val != "" {
			e = append(e, asciiEntry(s.tag, s.val))
		}
	}
	if !f.DateTime.IsZero() {
		e = append(e, asciiEntry(tagDateTime, f.DateTime.Format(exifDateLayout)))
	}
	if f.TimeCode != nil {
		e = append(e, entry{tagTimeCodes, typeByte, uint32(len(f.TimeCode)), f.TimeCode[:]})
	}
	if f.FrameRateN > 0 && f.FrameRateD > 0 {
		e = append(e, entry{tagFrameRate, typeSRational, 1, longs(f.FrameRateN, f.FrameRateD)})
	}
	return e
}

// tiffLayout is a DNG file before serialisation: IFD0, an optional EXIF
// sub-IFD and the image chunks that offsetsTag in IFD0 points at.
type tiffLayout struct {
	ifd0, exif []entry
	offsetsTag uint16
	chunks     [][]byte
}

// writeTIFF writes header | IFD0 + values | EXIF IFD + values | chunks.
func writeTIFF(w io.Writer, l tiffLayout) error {
	for _, ifd := range [][]entry{l.ifd0, l.exif} {
		sort.Slice(ifd, func(i, j int) bool { return ifd[i].tag < ifd[j].tag })
	}
	exifOff := tiffHeaderSize + ifdTotalSize(l.ifd0)
	chunkOff := exifOff
	if len(l.exif) > 0 {
		chunkOff += ifdTotalSize(l.exif)
		if err := patchLongs(l.ifd0, tagExifIFD, []int{exifOff}); err != nil {
			return err
		}
	}
	offsets := make([]int, len(l.chunks))
	for i, pos := 0, chunkOff; i < len(l.chunks); i++ {
		offsets[i] = pos
		pos += len(l.chunks[i])
	}
	if err := patchLongs(l.ifd0, l.offsetsTag, offsets); err != nil {
		return err
	}

	hdr := append([]byte(tiffLittleEndianMark), 0, 0, 0, 0, 0, 0)
	le.PutUint16(hdr[2:], tiffMagic)
	le.PutUint32(hdr[4:], tiffHeaderSize)
	ifd0, err := serializeIFD(l.ifd0, tiffHeaderSize)
	if err != nil {
		return err
	}
	exif, err := serializeIFD(l.exif, exifOff)
	if err != nil {
		return err
	}
	bw := bufio.NewWriterSize(w, writeBufferSize)
	for _, b := range append([][]byte{hdr, ifd0, exif}, l.chunks...) {
		if _, err := bw.Write(b); err != nil {
			return err
		}
	}
	return bw.Flush()
}

// ifdTotalSize is the size of an IFD plus its out-of-line values.
func ifdTotalSize(entries []entry) int {
	n := 2 + ifdEntrySize*len(entries) + 4
	for _, en := range entries {
		if len(en.data) > inlineValueSize {
			n += padEven(len(en.data))
		}
	}
	return n
}

// patchLongs overwrites the LONG values of tag with vals.
func patchLongs(entries []entry, tag uint16, vals []int) error {
	for i := range entries {
		if entries[i].tag == tag {
			if len(entries[i].data) != 4*len(vals) {
				return fmt.Errorf("dng: tag %d holds %d bytes, want %d", tag, len(entries[i].data), 4*len(vals))
			}
			copy(entries[i].data, longs(vals...))
			return nil
		}
	}
	return fmt.Errorf("dng: missing tag %d", tag)
}

// serializeIFD encodes entries as an IFD at file offset off, followed by
// the values that do not fit in an entry. An empty IFD encodes to nothing.
func serializeIFD(entries []entry, off int) ([]byte, error) {
	if len(entries) == 0 {
		return nil, nil
	}
	ifdSize := 2 + ifdEntrySize*len(entries) + 4
	out := make([]byte, ifdSize, ifdTotalSize(entries))
	le.PutUint16(out, uint16(len(entries)))
	for i, en := range entries {
		if int(en.count)*sizeOf(en.typ) != len(en.data) {
			return nil, fmt.Errorf("dng: tag %d has %d bytes for %d values", en.tag, len(en.data), en.count)
		}
		p := out[2+ifdEntrySize*i:]
		le.PutUint16(p, en.tag)
		le.PutUint16(p[2:], en.typ)
		le.PutUint32(p[4:], en.count)
		if len(en.data) <= inlineValueSize {
			copy(p[8:12], en.data)
			continue
		}
		le.PutUint32(p[8:], uint32(off+len(out)))
		out = append(out, en.data...)
		if len(out)%2 == 1 {
			out = append(out, 0) // TIFF values start on word boundaries
		}
	}
	return out, nil
}

func padEven(n int) int { return n + n%2 }

func sizeOf(typ uint16) int {
	switch typ {
	case typeByte, typeASCII, typeUndefined:
		return 1
	case typeShort:
		return 2
	case typeLong:
		return 4
	default:
		return 8
	}
}

func shorts(v ...int) []byte {
	b := make([]byte, 2*len(v))
	for i, x := range v {
		le.PutUint16(b[2*i:], uint16(x))
	}
	return b
}

func longs(v ...int) []byte {
	b := make([]byte, 4*len(v))
	for i, x := range v {
		le.PutUint32(b[4*i:], uint32(x))
	}
	return b
}

func srats(v ...float64) []byte {
	b := make([]byte, 8*len(v))
	for i, x := range v {
		le.PutUint32(b[8*i:], uint32(int32(math.Round(x*rationalDenominator))))
		le.PutUint32(b[8*i+4:], rationalDenominator)
	}
	return b
}

func urats(v ...float64) []byte {
	b := make([]byte, 8*len(v))
	for i, x := range v {
		le.PutUint32(b[8*i:], uint32(math.Round(x*rationalDenominator)))
		le.PutUint32(b[8*i+4:], rationalDenominator)
	}
	return b
}
