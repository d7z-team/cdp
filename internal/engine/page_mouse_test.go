package engine

import (
	"testing"
	"time"
)

func TestAllocateMousePhaseDurations(t *testing.T) {
	delays := allocateMousePhaseDurations(24, 96*time.Millisecond, 0.72)
	if len(delays) != 24 {
		t.Fatalf("expected 24 delays, got %d", len(delays))
	}
	total := time.Duration(0)
	for i, delay := range delays {
		if delay < minMouseStepDelay {
			t.Fatalf("delay %d below minimum: %s", i, delay)
		}
		if delay > maxMouseStepDelay {
			t.Fatalf("delay %d above maximum: %s", i, delay)
		}
		total += delay
	}
	if total < 72*time.Millisecond || total > 140*time.Millisecond {
		t.Fatalf("unexpected total allocated duration: %s", total)
	}
	if delays[len(delays)-1] < delays[0] {
		t.Fatalf("expected tail-biased allocation, first=%s last=%s", delays[0], delays[len(delays)-1])
	}
}

func TestMouseWheelEventParams(t *testing.T) {
	params := mouseWheelEventParams(12.5, 24.25, -8, 120)
	if params["type"] != "mouseWheel" || params["x"] != 12.5 || params["y"] != 24.25 || params["deltaX"] != float64(-8) || params["deltaY"] != float64(120) {
		t.Fatalf("unexpected wheel params: %#v", params)
	}
	if params["button"] != "none" || params["buttons"] != 0 {
		t.Fatalf("unexpected wheel button state: %#v", params)
	}
}

func TestBuildHumanMouseMoveStepsDenseSampling(t *testing.T) {
	page := &Page{}
	steps := page.buildHumanMouseMoveSteps(120, 120, 620, 340, 1600, 1200)
	if steps == nil {
		t.Fatal("expected dense human mouse steps, got nil")
	}
	if len(steps) < 10 || len(steps) > 18 {
		t.Fatalf("expected medium-distance sampling to stay compact, got %d steps", len(steps))
	}
	total := time.Duration(0)
	for i, step := range steps {
		if step.x < 0 || step.x > 1600 || step.y < 0 || step.y > 1200 {
			t.Fatalf("step %d out of bounds: %+v", i, step)
		}
		total += step.delay
	}
	if total < minMouseMotionBudget || total > maxMouseMotionBudget {
		t.Fatalf("unexpected movement duration budget: %s", total)
	}
	last := steps[len(steps)-1]
	if last.delay < minMouseStepDelay || last.delay > maxMouseStepDelay {
		t.Fatalf("unexpected final step delay: %s", last.delay)
	}
}

func TestBuildHumanMouseMoveStepsStayWithinMoveBudgetCap(t *testing.T) {
	page := &Page{}
	steps := page.buildHumanMouseMoveSteps(120, 120, 1480, 920, 1600, 1200)
	if steps == nil {
		t.Fatal("expected long-distance human mouse steps, got nil")
	}
	total := time.Duration(0)
	for _, step := range steps {
		total += step.delay
	}
	if total > maxMouseMotionBudget {
		t.Fatalf("expected motion budget <= %s, got %s", maxMouseMotionBudget, total)
	}
	if total > maxMouseMoveTotal {
		t.Fatalf("expected full move budget cap <= %s, got %s", maxMouseMoveTotal, total)
	}
	if len(steps) < 18 || len(steps) > 26 {
		t.Fatalf("expected long-distance sampling to stay bounded, got %d steps", len(steps))
	}
}

func TestTargetMouseStepCountRanges(t *testing.T) {
	for i := 0; i < 12; i++ {
		if count := targetMouseStepCount(90); count < 7 || count > 10 {
			t.Fatalf("unexpected short-distance count: %d", count)
		}
		if count := targetMouseStepCount(320); count < 10 || count > 14 {
			t.Fatalf("unexpected medium-distance count: %d", count)
		}
		if count := targetMouseStepCount(780); count < 14 || count > 20 {
			t.Fatalf("unexpected long-distance count: %d", count)
		}
		if count := targetMouseStepCount(1280); count < 18 || count > 24 {
			t.Fatalf("unexpected extra-long-distance count: %d", count)
		}
	}
}
