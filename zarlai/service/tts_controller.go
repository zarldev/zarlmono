package service

import (
	"context"
	"fmt"
	"sync"
)

// VoiceEngine is the producer-side union of synthesizer types
// VoiceController can hold. Exported so cmd/zarl can build the engine
// map; consumers see narrower views (service.Synthesizer for the chat
// path, transport/grpc's local interface for admin).
type VoiceEngine interface {
	Synthesize(ctx context.Context, text string) ([]int16, error)
	SampleRate() int
	Speaker() int
	Speed() float32
	NumSpeakers() int
	Tune(speaker int, speed float32)
	Close()
}

// VoiceController owns one or more TTS engines, exposes a single active
// engine to the chat path, and lets the admin path switch between them at
// runtime. All engines remain loaded — switching just retargets which one
// answers Synthesize.
type VoiceController struct {
	mu      sync.RWMutex
	engines map[EngineName]VoiceEngine
	order   []EngineName
	active  EngineName
}

// NewVoiceController constructs a controller with the given engines. The
// first engine in `order` whose entry is non-nil becomes active.
// Engines passed as nil are skipped (graceful degradation when their model
// bundle is missing on disk).
func NewVoiceController(order []EngineName, engines map[EngineName]VoiceEngine) (*VoiceController, error) {
	live := make(map[EngineName]VoiceEngine, len(engines))
	liveOrder := make([]EngineName, 0, len(order))
	for _, name := range order {
		if e := engines[name]; e != nil {
			live[name] = e
			liveOrder = append(liveOrder, name)
		}
	}
	if len(live) == 0 {
		return nil, fmt.Errorf("voice controller: no engines available")
	}
	return &VoiceController{
		engines: live,
		order:   liveOrder,
		active:  liveOrder[0],
	}, nil
}

func (c *VoiceController) activeEngine() VoiceEngine {
	return c.engines[c.active]
}

// Synthesize satisfies service.Synthesizer.
func (c *VoiceController) Synthesize(ctx context.Context, text string) ([]int16, error) {
	c.mu.RLock()
	e := c.activeEngine()
	c.mu.RUnlock()
	return e.Synthesize(ctx, text)
}

// SampleRate satisfies service.Synthesizer.
func (c *VoiceController) SampleRate() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.activeEngine().SampleRate()
}

// Close satisfies service.Synthesizer — closes every loaded engine.
func (c *VoiceController) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, e := range c.engines {
		e.Close()
	}
}

// Speaker, Speed, NumSpeakers, Tune all act on the active engine.
func (c *VoiceController) Speaker() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.activeEngine().Speaker()
}

func (c *VoiceController) Speed() float32 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.activeEngine().Speed()
}

func (c *VoiceController) NumSpeakers() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.activeEngine().NumSpeakers()
}

func (c *VoiceController) Tune(speaker int, speed float32) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	c.activeEngine().Tune(speaker, speed)
}

// TuneEngine sets a specific engine's voice without making it active.
// Used at startup to restore each engine's persisted speaker/speed.
func (c *VoiceController) TuneEngine(name EngineName, speaker int, speed float32) error {
	c.mu.RLock()
	defer c.mu.RUnlock()
	e, ok := c.engines[name]
	if !ok {
		return fmt.Errorf("voice controller: engine %q not loaded", name)
	}
	e.Tune(speaker, speed)
	return nil
}

// Engine returns the active engine name.
func (c *VoiceController) Engine() EngineName {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.active
}

// Engines returns the names of all loaded engines, in the order passed at
// construction.
func (c *VoiceController) Engines() []EngineName {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]EngineName, len(c.order))
	copy(out, c.order)
	return out
}

// SwitchEngine makes `name` the active engine. The engine retains whatever
// speaker/speed it was last tuned to, so switching back-and-forth preserves
// each engine's voice independently.
func (c *VoiceController) SwitchEngine(name EngineName) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.engines[name]; !ok {
		return fmt.Errorf("voice controller: engine %q not loaded", name)
	}
	c.active = name
	return nil
}
