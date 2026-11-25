package main

import (
	"bytes"
	"image"
	"image/color"
	"image/gif"
	"testing"
	"time"
)

func TestDecodeCoffeeGIF(t *testing.T) {
	frames, err := decodeGIF(coffeeGIF, supportIconPixels)
	if err != nil {
		t.Fatal(err)
	}
	if len(frames.images) < 2 {
		t.Fatalf("%d frames, want an animation", len(frames.images))
	}
	if b := frames.images[0].Bounds(); b.Dy() != supportIconPixels {
		t.Errorf("frame height %d, want %d", b.Dy(), supportIconPixels)
	}
	if frames.duration() <= 0 {
		t.Errorf("loop duration %v", frames.duration())
	}
}

func TestGIFFrameTiming(t *testing.T) {
	palette := color.Palette{color.Transparent, color.White}
	g := &gif.GIF{Delay: []int{5, 0, 20}} // 50 ms, default 100 ms, 200 ms
	for range g.Delay {
		g.Image = append(g.Image, image.NewPaletted(image.Rect(0, 0, 4, 4), palette))
	}
	var buf bytes.Buffer
	if err := gif.EncodeAll(&buf, g); err != nil {
		t.Fatal(err)
	}
	frames, err := decodeGIF(buf.Bytes(), 2)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		at   time.Duration
		want int
	}{{0, 0}, {49 * time.Millisecond, 0}, {50 * time.Millisecond, 1}, {149 * time.Millisecond, 1}, {150 * time.Millisecond, 2}, {time.Hour, 2}} {
		if got := frames.at(tc.at); got != tc.want {
			t.Errorf("frame at %v = %d, want %d", tc.at, got, tc.want)
		}
	}
	if d := frames.duration(); d != 350*time.Millisecond {
		t.Errorf("duration %v, want 350ms", d)
	}
}

func TestDecodeGIFRejectsGarbage(t *testing.T) {
	if _, err := decodeGIF([]byte("not a gif"), 2); err == nil {
		t.Error("garbage accepted")
	}
}
