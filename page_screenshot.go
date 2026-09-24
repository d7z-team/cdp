package cdp

import (
	"context"
	"image"
	"sync"

	engine "gopkg.d7z.net/cdp/internal/engine"
	"gopkg.d7z.net/cdp/internal/imageutil"
)

func (p *Page) Screenshot(ctx context.Context) (*image.RGBA, error) {
	c, cancel, err := p.operation(ctx, p.timeouts().Screenshot)
	if err != nil {
		return nil, err
	}
	defer cancel()
	v, err := p.engine.ScreenshotContext(c)
	return v, operationError("screenshot", err)
}
func (l *Locator) Screenshot(ctx context.Context) (*image.RGBA, error) {
	c, cancel, err := l.operation(ctx, l.timeouts().Screenshot)
	if err != nil {
		return nil, l.wrapError("screenshot", err)
	}
	defer cancel()
	e, err := l.resolve(c, "")
	if err != nil {
		return nil, l.wrapError("screenshot", err)
	}
	value, err := e.Screenshot(c)
	return value, l.wrapError("screenshot", err)
}
func (e *Element) ImageSource(ctx context.Context) (*image.RGBA, error) {
	c, cancel, err := e.operation(ctx, e.timeouts().Read)
	if err != nil {
		return nil, err
	}
	defer cancel()
	if err = e.validate(c); err != nil {
		return nil, err
	}
	data, err := e.page.engine.SelectorTargetImageData(c, e.ref)
	if err != nil {
		return nil, operationError("image_source", err)
	}
	v, err := imageutil.Decode(data)
	return v, operationError("image_source.decode", err)
}
func (l *Locator) ImageSource(ctx context.Context) (*image.RGBA, error) {
	c, cancel, err := l.operation(ctx, l.timeouts().Read)
	if err != nil {
		return nil, l.wrapError("image_source", err)
	}
	defer cancel()
	e, err := l.resolve(c, "")
	if err != nil {
		return nil, l.wrapError("image_source", err)
	}
	value, err := e.ImageSource(c)
	return value, l.wrapError("image_source", err)
}

type ScreencastOptions struct {
	Format                                      string
	Quality, MaxWidth, MaxHeight, EveryNthFrame int
}
type ScreencastFrame struct {
	Data     []byte
	Sequence uint64
	Metadata ScreencastMetadata
}
type ScreencastMetadata struct{ OffsetTop, PageScaleFactor, DeviceWidth, DeviceHeight, ScrollOffsetX, ScrollOffsetY, Timestamp float64 }
type Screencast struct {
	life   context.Context
	cancel context.CancelFunc
	page   *Page
	owner  engine.ScreencastOwner
	once   sync.Once
	err    error
}

func (p *Page) Screencast(ctx context.Context, opts ScreencastOptions) (*Screencast, error) {
	c, cancel, err := p.operation(ctx, p.timeouts().Read)
	if err != nil {
		return nil, err
	}
	defer cancel()
	owner := p.engine.NewScreencastOwner()
	err = p.engine.EnsureScreencastFor(c, owner, engine.ScreencastOptions(opts))
	if err != nil {
		return nil, operationError("screencast.start", err)
	}
	life, stop := context.WithCancel(context.Background())
	return &Screencast{page: p, owner: owner, life: life, cancel: stop}, nil
}
func (s *Screencast) Next(ctx context.Context, after uint64) (ScreencastFrame, error) {
	if s == nil || s.life == nil || s.life.Err() != nil {
		return ScreencastFrame{}, ErrClosed
	}
	c, cancel, err := s.page.operation(ctx, s.page.timeouts().Read)
	if err != nil {
		return ScreencastFrame{}, err
	}
	defer cancel()
	stop := context.AfterFunc(s.life, cancel)
	defer stop()
	f, err := s.page.engine.WaitScreencastFrame(c, after, false)
	return ScreencastFrame{Data: append([]byte(nil), f.Data...), Sequence: f.Sequence, Metadata: ScreencastMetadata(f.Metadata)}, operationError("screencast.next", err)
}
func (s *Screencast) Close() error {
	if s == nil || s.cancel == nil {
		return nil
	}
	s.once.Do(func() {
		s.cancel()
		ctx, cancel := context.WithTimeout(context.Background(), s.page.timeouts().Shutdown)
		defer cancel()
		s.err = s.page.engine.StopScreencastFor(ctx, s.owner)
	})
	return s.err
}

func (p *Page) ScreencastSequence() uint64 {
	if p == nil || p.engine == nil {
		return 0
	}
	return p.engine.ScreencastSequence()
}
