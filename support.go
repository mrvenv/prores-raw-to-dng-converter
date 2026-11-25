package main

import (
	"bytes"
	_ "embed"
	"errors"
	"fmt"
	"image"
	"image/draw"
	"image/gif"
	"net/url"
	"sort"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/widget"
	xdraw "golang.org/x/image/draw"
)

// coffeeGIF is the animated Buy Me a Coffee logo shown in the support strip.
//
//go:embed assets/coffee.gif
var coffeeGIF []byte

// Support strip text and link.
const (
	supportText     = "Free and open source. If it saves you time,"
	supportLinkText = "buy me a coffee"
	supportURL      = "https://buymeacoffee.com/tsofindavid"
)

const (
	// supportIconSize is the icon's height in Fyne units, about one text line.
	supportIconSize float32 = 28
	// supportIconPixels is the height frames are scaled to: twice the size,
	// so the icon stays sharp on HiDPI screens. Keeping every frame of the
	// 480-pixel source would take about 90 MB.
	supportIconPixels = 2 * 28
	// gifDelayUnit is the unit of GIF frame delays (hundredths of a second).
	gifDelayUnit = 10 * time.Millisecond
	// defaultGIFDelay replaces a zero delay, as browsers do.
	defaultGIFDelay = 10 * gifDelayUnit
)

// supportFooter is the strip at the bottom of the window that links to the
// author's donation page: the animated logo, a line of text and a link.
func (u *ui) supportFooter() fyne.CanvasObject {
	link, err := url.Parse(supportURL)
	if err != nil {
		panic(fmt.Sprintf("supportURL %q: %v", supportURL, err)) // a constant: a programmer error
	}
	icon := newSupportIcon(func() {
		if err := u.app.OpenURL(link); err != nil {
			u.logf("open %s: %v", link, err)
		}
	})
	// Decoding and scaling every frame takes most of a second; the window
	// does not wait for it, the logo appears when it is ready.
	go func() {
		frames, err := decodeGIF(coffeeGIF, supportIconPixels)
		if err != nil {
			// The strip still works as text; the log says why the logo is missing.
			u.logf("support icon: %v", err)
			return
		}
		fyne.Do(func() { icon.play(frames) })
	}()
	return container.NewCenter(container.NewHBox(
		icon,
		widget.NewLabel(supportText),
		widget.NewHyperlink(supportLinkText, link),
	))
}

// gifFrames is a decoded animation: whole frames, each composed over the
// ones before it as the GIF's disposal methods say, and when each one ends.
type gifFrames struct {
	images []image.Image
	ends   []time.Duration // end of frame i, from the start of the loop
}

// duration is the length of one loop.
func (g gifFrames) duration() time.Duration { return g.ends[len(g.ends)-1] }

// at is the index of the frame shown at time t into the loop.
func (g gifFrames) at(t time.Duration) int {
	i := sort.Search(len(g.ends), func(i int) bool { return g.ends[i] > t })
	return min(i, len(g.ends)-1)
}

// decodeGIF decodes an animated GIF into whole frames scaled to the given
// height.
func decodeGIF(data []byte, height int) (gifFrames, error) {
	g, err := gif.DecodeAll(bytes.NewReader(data))
	if err != nil {
		return gifFrames{}, fmt.Errorf("decode gif: %w", err)
	}
	if len(g.Image) == 0 || g.Config.Width <= 0 || g.Config.Height <= 0 {
		return gifFrames{}, errors.New("decode gif: no frames")
	}
	bounds := image.Rect(0, 0, g.Config.Width, g.Config.Height)
	scaled := image.Rect(0, 0, max(1, height*g.Config.Width/g.Config.Height), height)
	canvasImage := image.NewRGBA(bounds)
	var out gifFrames
	var elapsed time.Duration
	for i, frame := range g.Image {
		disposal := frameDisposal(g, i)
		var previous *image.RGBA
		if disposal == gif.DisposalPrevious {
			previous = cloneRGBA(canvasImage)
		}
		draw.Draw(canvasImage, frame.Bounds(), frame, frame.Bounds().Min, draw.Over)

		small := image.NewRGBA(scaled)
		xdraw.BiLinear.Scale(small, scaled, canvasImage, bounds, xdraw.Src, nil)
		elapsed += frameDelay(g, i)
		out.images = append(out.images, small)
		out.ends = append(out.ends, elapsed)

		switch disposal {
		case gif.DisposalBackground:
			draw.Draw(canvasImage, frame.Bounds(), image.Transparent, image.Point{}, draw.Src)
		case gif.DisposalPrevious:
			canvasImage = previous
		}
	}
	return out, nil
}

func frameDisposal(g *gif.GIF, i int) byte {
	if i < len(g.Disposal) {
		return g.Disposal[i]
	}
	return gif.DisposalNone
}

func frameDelay(g *gif.GIF, i int) time.Duration {
	if i < len(g.Delay) && g.Delay[i] > 0 {
		return time.Duration(g.Delay[i]) * gifDelayUnit
	}
	return defaultGIFDelay
}

func cloneRGBA(src *image.RGBA) *image.RGBA {
	dst := image.NewRGBA(src.Bounds())
	copy(dst.Pix, src.Pix)
	return dst
}

// supportIcon is the animated logo; tapping it opens the donation page.
type supportIcon struct {
	widget.BaseWidget
	image *canvas.Image
	onTap func()
}

// newSupportIcon is an empty slot of the logo's size until play is called,
// so the strip's layout does not jump when the logo appears.
func newSupportIcon(onTap func()) *supportIcon {
	img := canvas.NewImageFromImage(nil)
	img.FillMode = canvas.ImageFillContain
	img.SetMinSize(fyne.NewSquareSize(supportIconSize))
	icon := &supportIcon{image: img, onTap: onTap}
	icon.ExtendBaseWidget(icon)
	return icon
}

// play shows frames, looping if there are several. Call it on the UI thread.
func (s *supportIcon) play(frames gifFrames) {
	s.image.Image = frames.images[0]
	s.image.Refresh()
	if len(frames.images) > 1 {
		animate(s.image, frames).Start()
	}
}

// CreateRenderer implements fyne.Widget.
func (s *supportIcon) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(s.image)
}

// Tapped implements fyne.Tappable.
func (s *supportIcon) Tapped(*fyne.PointEvent) { s.onTap() }

// Cursor implements desktop.Cursorable: the hand, as over a link.
func (s *supportIcon) Cursor() desktop.Cursor { return desktop.PointerCursor }

// animate loops frames on img. Fyne runs animation ticks on the UI thread,
// so the image is swapped there and nothing else needs a goroutine.
func animate(img *canvas.Image, frames gifFrames) *fyne.Animation {
	total := frames.duration()
	current := 0
	a := fyne.NewAnimation(total, func(progress float32) {
		i := frames.at(time.Duration(float64(progress) * float64(total)))
		if i == current {
			return
		}
		current = i
		img.Image = frames.images[i]
		img.Refresh()
	})
	a.Curve = fyne.AnimationLinear
	a.RepeatCount = fyne.AnimationRepeatForever
	return a
}
