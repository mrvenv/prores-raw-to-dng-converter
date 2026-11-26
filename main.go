// Command rawconv is a desktop app that converts ProRes RAW clips into
// CinemaDNG sequences for DaVinci Resolve.
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/storage"
	"fyne.io/fyne/v2/widget"

	"rawconv/internal/convert"
	"rawconv/internal/dng"
	"rawconv/internal/ffmpeg"
	"rawconv/internal/version"
)

// Preference keys.
const (
	prefCompression = "compression" // the chosen dng.Compression, as its String
	prefOutDir      = "outDir"
)

// Window and widget sizes.
const (
	windowWidth     = 820
	windowHeight    = 640
	logRowsVisible  = 6
	progressRefresh = 200 * time.Millisecond
)

// UI text.
const (
	appID             = "com.rawconv.app" // must match ID in FyneApp.toml
	windowTitleFormat = "ProRes Raw to DNG Converter %s"
	lookingForFFmpeg  = "Looking for ffmpeg…"
	dropHint          = "Drop .mov files or folders here"
	addClipsLabel     = "Add clips…"
	addFolderLabel    = "Add folder…"
	clearLabel        = "Clear"
	chooseLabel       = "Choose…"
	outputLabel       = "Output:"
	audioLabel        = "Extract audio (WAV)"
	compressionLabel  = "Compression:"
	convertLabel      = "Convert"
	cancelLabel       = "Cancel"
	noOutputTitle     = "Output"
	noOutputMessage   = "Choose an output folder first."
	defaultOutSubdir  = "CinemaDNG"
	moviesDir         = "Movies"
	appleDoublePrefix = "._" // macOS resource-fork files next to clips on non-HFS volumes
	statusQueued      = "queued"
	statusConverting  = "converting…"
	statusDone        = "done"
	statusCancelled   = "cancelled"
	statusError       = "error"
	clipDoneFormat    = "✓ %s → %s (%d frames in %s, %.1f fps)"
	clipErrorFormat   = "ERROR %s: %v"
	finishedFormat    = "finished: %d of %d clips converted in %s"
	estimateFormat    = "≈ %s"
	unmeasuredFormat  = " (+%d not measured)"
	probeErrorFormat  = "cannot read %s: %v"
	ffmpegFoundFormat = "ffmpeg: %s  (%s)"
	ffmpegMissingIcon = "⚠ "
	placeholderName   = "clip"
	placeholderStatus = "status"
)

// clipExtensions are the containers ProRes RAW is recorded in.
var clipExtensions = []string{".mov", ".mp4"}

func compressionLabels() []string {
	labels := make([]string, len(dng.Compressions))
	for i, c := range dng.Compressions {
		labels[i] = c.Label()
	}
	return labels
}

// compressionByLabel maps a select label back to its mode; unknown labels
// fall back to the default.
func compressionByLabel(label string) dng.Compression {
	for _, c := range dng.Compressions {
		if c.Label() == label {
			return c
		}
	}
	return dng.LosslessJPEG12
}

type clip struct {
	path     string
	status   string
	progress float64
	// probe is filled in the background once ffmpeg is found, for the size
	// estimate; nil until then or when the clip cannot be read.
	probe *ffmpeg.Probe
}

type ui struct {
	app    fyne.App
	win    fyne.Window
	mu     sync.Mutex
	clips  []*clip
	list   *widget.List
	outDir *widget.Entry
	audio  *widget.Check
	// compression offers dng.Compressions by Label; the choice persists
	// under prefCompression as the mode's String.
	compression *widget.Select
	// estimate shows the expected size of the whole list in the chosen
	// compression.
	estimate     *widget.Label
	start        *widget.Button
	cancel       *widget.Button
	addBtn       *widget.Button
	addFolderBtn *widget.Button
	clearBtn     *widget.Button
	total        *widget.ProgressBar
	logBox       *widget.Entry
	status       *widget.Label

	tools   ffmpeg.Tools
	toolErr error
	stop    context.CancelFunc
}

func main() {
	a := app.NewWithID(appID)
	u := &ui{app: a, win: a.NewWindow(fmt.Sprintf(windowTitleFormat, version.Version))}
	u.build()
	u.win.Resize(fyne.NewSize(windowWidth, windowHeight))
	go u.detectFFmpeg()
	u.win.ShowAndRun()
}

func (u *ui) build() {
	u.status = widget.NewLabel(lookingForFFmpeg)
	u.status.Wrapping = fyne.TextWrapWord

	u.list = widget.NewList(
		func() int { u.mu.Lock(); defer u.mu.Unlock(); return len(u.clips) },
		func() fyne.CanvasObject {
			name := widget.NewLabel(placeholderName)
			name.Truncation = fyne.TextTruncateEllipsis
			st := widget.NewLabel(placeholderStatus)
			pb := widget.NewProgressBar()
			return container.NewVBox(container.NewBorder(nil, nil, nil, st, name), pb)
		},
		func(id widget.ListItemID, o fyne.CanvasObject) {
			u.mu.Lock()
			if id >= len(u.clips) {
				u.mu.Unlock()
				return
			}
			c := *u.clips[id]
			u.mu.Unlock()
			box := o.(*fyne.Container)
			row := box.Objects[0].(*fyne.Container)
			row.Objects[0].(*widget.Label).SetText(filepath.Base(c.path))
			row.Objects[1].(*widget.Label).SetText(c.status)
			box.Objects[1].(*widget.ProgressBar).SetValue(c.progress)
		},
	)
	// Rows are status displays only; selecting one does nothing.
	u.list.OnSelected = func(widget.ListItemID) { u.list.UnselectAll() }

	u.addBtn = widget.NewButton(addClipsLabel, u.addFiles)
	u.addFolderBtn = widget.NewButton(addFolderLabel, u.addFolder)
	u.clearBtn = widget.NewButton(clearLabel, func() {
		u.mu.Lock()
		u.clips = nil
		u.mu.Unlock()
		u.list.Refresh()
		u.updateEstimate()
	})

	u.outDir = widget.NewEntry()
	u.outDir.SetText(u.app.Preferences().StringWithFallback(prefOutDir, defaultOutDir()))
	u.outDir.OnChanged = func(s string) { u.app.Preferences().SetString(prefOutDir, s) }
	browse := widget.NewButton(chooseLabel, func() {
		d := dialog.NewFolderOpen(func(l fyne.ListableURI, err error) {
			if err == nil && l != nil {
				u.outDir.SetText(l.Path())
			}
		}, u.win)
		if l, err := storage.ListerForURI(storage.NewFileURI(u.outDir.Text)); err == nil {
			d.SetLocation(l)
		}
		d.Show()
	})
	u.audio = widget.NewCheck(audioLabel, nil)
	u.audio.SetChecked(true)
	u.estimate = widget.NewLabel("")
	u.compression = widget.NewSelect(compressionLabels(), func(label string) {
		u.app.Preferences().SetString(prefCompression, compressionByLabel(label).String())
		u.updateEstimate()
	})
	saved, err := dng.ParseCompression(u.app.Preferences().StringWithFallback(prefCompression, dng.LosslessJPEG12.String()))
	if err != nil {
		saved = dng.LosslessJPEG12 // stale or hand-edited preference
	}
	u.compression.SetSelected(saved.Label())

	u.start = widget.NewButton(convertLabel, u.run)
	u.start.Importance = widget.HighImportance
	u.start.Disable()
	u.cancel = widget.NewButton(cancelLabel, func() {
		if u.stop != nil {
			u.stop()
		}
	})
	u.cancel.Disable()
	u.total = widget.NewProgressBar()

	u.logBox = widget.NewMultiLineEntry()
	u.logBox.Wrapping = fyne.TextWrapWord
	u.logBox.SetMinRowsVisible(logRowsVisible)

	top := container.NewVBox(
		u.status,
		container.NewHBox(u.addBtn, u.addFolderBtn, u.clearBtn),
	)
	hint := widget.NewLabel(dropHint)
	hint.Alignment = fyne.TextAlignCenter
	listArea := container.NewStack(container.NewCenter(hint), u.list)
	bottom := container.NewVBox(
		container.NewBorder(nil, nil, widget.NewLabel(outputLabel), browse, u.outDir),
		container.NewHBox(u.audio, widget.NewLabel(compressionLabel), u.compression, u.estimate, layout.NewSpacer(), u.cancel, u.start),
		u.total,
		u.logBox,
		widget.NewSeparator(),
		u.supportFooter(),
	)
	u.win.SetContent(container.NewPadded(container.NewBorder(top, bottom, nil, nil, listArea)))

	u.win.SetOnDropped(func(_ fyne.Position, uris []fyne.URI) {
		for _, uri := range uris {
			u.addPath(uri.Path())
		}
		u.list.Refresh()
		u.probeClips()
	})
}

// updateEstimate shows the expected size of every measured clip in the
// chosen compression. Call it on the UI thread.
func (u *ui) updateEstimate() {
	compression := compressionByLabel(u.compression.Selected)
	var total int64
	unmeasured := 0
	u.mu.Lock()
	for _, c := range u.clips {
		if c.probe == nil {
			unmeasured++
			continue
		}
		total += convert.EstimateSize(*c.probe, compression, 0)
	}
	u.mu.Unlock()
	if total == 0 {
		u.estimate.SetText("")
		return
	}
	text := fmt.Sprintf(estimateFormat, convert.FormatSize(total))
	if unmeasured > 0 {
		text += fmt.Sprintf(unmeasuredFormat, unmeasured)
	}
	u.estimate.SetText(text)
}

// probeClips reads the size and frame count of every clip not measured
// yet, in the background, and refreshes the estimate as each one arrives.
// Call it on the UI thread, once ffmpeg is found.
func (u *ui) probeClips() {
	if u.toolErr != nil || u.tools.FFprobe == "" {
		return
	}
	u.mu.Lock()
	var todo []*clip
	for _, c := range u.clips {
		if c.probe == nil {
			todo = append(todo, c)
		}
	}
	u.mu.Unlock()
	tools := u.tools
	go func() {
		for _, c := range todo {
			p, err := tools.ProbeFile(context.Background(), c.path)
			if err != nil {
				u.logf(probeErrorFormat, filepath.Base(c.path), err)
				continue
			}
			u.mu.Lock()
			c.probe = &p
			u.mu.Unlock()
			fyne.Do(u.updateEstimate)
		}
	}()
}

// defaultOutDir is ~/Movies/CinemaDNG, or CinemaDNG in the working
// directory when the home directory is unknown.
func defaultOutDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return defaultOutSubdir
	}
	return filepath.Join(home, moviesDir, defaultOutSubdir)
}

func (u *ui) logf(format string, a ...any) {
	line := fmt.Sprintf(format, a...)
	fyne.Do(func() {
		u.logBox.Append(line + "\n")
		u.logBox.CursorRow = strings.Count(u.logBox.Text, "\n")
	})
}

func (u *ui) detectFFmpeg() {
	t, err := ffmpeg.Find(context.Background())
	fyne.Do(func() {
		u.tools, u.toolErr = t, err
		if err != nil {
			u.status.SetText(ffmpegMissingIcon + err.Error())
			return
		}
		u.status.SetText(fmt.Sprintf(ffmpegFoundFormat, t.FFmpeg, t.Version))
		u.start.Enable()
		u.probeClips()
	})
}

func isClip(p string) bool {
	return slices.Contains(clipExtensions, strings.ToLower(filepath.Ext(p))) &&
		!strings.HasPrefix(filepath.Base(p), appleDoublePrefix)
}

// addPath queues a clip, or every clip under a folder. Unreadable paths are
// logged, not fatal: the rest of a dropped batch still gets queued.
func (u *ui) addPath(p string) {
	st, err := os.Stat(p)
	if err != nil {
		u.logf("skip %s: %v", p, err)
		return
	}
	var files []string
	if st.IsDir() {
		err := filepath.WalkDir(p, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				u.logf("skip %s: %v", path, err)
				return nil
			}
			if !d.IsDir() && isClip(path) {
				files = append(files, path)
			}
			return nil
		})
		if err != nil {
			u.logf("scan %s: %v", p, err)
		}
	} else if isClip(p) {
		files = []string{p}
	}
	u.mu.Lock()
	defer u.mu.Unlock()
outer:
	for _, f := range files {
		for _, c := range u.clips {
			if c.path == f {
				continue outer
			}
		}
		u.clips = append(u.clips, &clip{path: f, status: statusQueued})
	}
}

func (u *ui) addFiles() {
	d := dialog.NewFileOpen(func(r fyne.URIReadCloser, err error) {
		if err != nil || r == nil {
			return
		}
		// Only the path is needed; the file was opened read-only, so a
		// close error loses nothing.
		_ = r.Close()
		u.addPath(r.URI().Path())
		u.list.Refresh()
		u.probeClips()
	}, u.win)
	d.SetFilter(storage.NewExtensionFileFilter(clipFilterExtensions()))
	d.Show()
}

// clipFilterExtensions lists clipExtensions in both cases, as the file
// dialog filter matches case-sensitively.
func clipFilterExtensions() []string {
	var exts []string
	for _, e := range clipExtensions {
		exts = append(exts, e, strings.ToUpper(e))
	}
	return exts
}

func (u *ui) addFolder() {
	dialog.ShowFolderOpen(func(l fyne.ListableURI, err error) {
		if err == nil && l != nil {
			u.addPath(l.Path())
			u.list.Refresh()
			u.probeClips()
		}
	}, u.win)
}

func (u *ui) setBusy(busy bool) {
	listButtons := []*widget.Button{u.addBtn, u.addFolderBtn, u.clearBtn}
	if busy {
		u.start.Disable()
		for _, b := range listButtons {
			b.Disable()
		}
		u.cancel.Enable()
		return
	}
	if u.toolErr == nil {
		u.start.Enable()
	}
	for _, b := range listButtons {
		b.Enable()
	}
	u.cancel.Disable()
}

func (u *ui) run() {
	out := strings.TrimSpace(u.outDir.Text)
	if out == "" {
		dialog.ShowInformation(noOutputTitle, noOutputMessage, u.win)
		return
	}
	// Every click converts the whole list, finished clips included, so a
	// clip can be converted again with other settings.
	u.mu.Lock()
	queue := slices.Clone(u.clips)
	for _, c := range queue {
		c.status, c.progress = statusQueued, 0
	}
	u.mu.Unlock()
	u.list.Refresh()
	if len(queue) == 0 {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	u.stop = cancel
	u.setBusy(true)
	u.total.SetValue(0)
	audio := u.audio.Checked
	compression := compressionByLabel(u.compression.Selected)

	go func() {
		defer cancel()
		ok := 0
		runStart := time.Now()
		for i, c := range queue {
			if ctx.Err() != nil {
				break
			}
			u.update(c, statusConverting, 0)
			start := time.Now()
			var last time.Time
			frac := 0.0
			res, err := convert.Convert(ctx, u.tools, c.path, convert.Options{
				OutDir: out, Audio: audio, Compression: compression,
				Log: func(s string) { u.logf("%s", s) },
				Progress: func(done, total int) {
					if time.Since(last) < progressRefresh && done != total {
						return
					}
					last = time.Now()
					if total > 0 {
						frac = float64(done) / float64(total)
					}
					p := convert.Progress{Done: done, Total: total, Elapsed: time.Since(start)}
					u.update(c, p.String(), frac)
					overall := (float64(i) + frac) / float64(len(queue))
					fyne.Do(func() { u.total.SetValue(overall) })
				},
			})
			switch {
			case ctx.Err() != nil:
				u.update(c, statusCancelled, frac)
			case err != nil:
				u.update(c, statusError, frac)
				u.logf(clipErrorFormat, filepath.Base(c.path), err)
			default:
				ok++
				el := time.Since(start)
				u.update(c, statusDone, 1)
				u.logf(clipDoneFormat, filepath.Base(c.path), res.OutDir,
					res.Frames, convert.FormatDuration(el), float64(res.Frames)/el.Seconds())
			}
		}
		n := len(queue)
		cancelled := ctx.Err() != nil
		fyne.Do(func() {
			u.setBusy(false)
			if !cancelled {
				u.total.SetValue(1)
			}
		})
		u.logf(finishedFormat, ok, n, convert.FormatDuration(time.Since(runStart)))
	}()
}

func (u *ui) update(c *clip, status string, progress float64) {
	u.mu.Lock()
	c.status, c.progress = status, progress
	u.mu.Unlock()
	fyne.Do(func() { u.list.Refresh() })
}
