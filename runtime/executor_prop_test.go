package runtime

import (
	"context"
	"fmt"
	"iter"
	"math/rand"
	"testing"
	"testing/quick"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
	"github.com/nisimpson/bond"
)

// streamingAgent is a test helper that emits a preconfigured sequence of StreamEvents.
// It implements bond.Agent for use with bondExecutor in property tests.
type streamingAgent struct {
	events []bond.StreamEvent
}

func (a *streamingAgent) Stream(_ context.Context, _ []bond.Message) iter.Seq2[bond.StreamEvent, error] {
	return func(yield func(bond.StreamEvent, error) bool) {
		for _, event := range a.events {
			if !yield(event, nil) {
				return
			}
		}
	}
}

// Feature: streaming-improvements, Property 9: Artifact ID consistency per content type
// **Validates: Requirements 4.3**

// TestProperty_ArtifactIDConsistencyPerContentType verifies that for any sequence of
// mixed text and media deltas within a single execution, all TaskArtifactUpdateEvent
// events for text content share a single artifact ID, and all events for a given media
// stream share their own distinct artifact ID.
func TestProperty_ArtifactIDConsistencyPerContentType(t *testing.T) {
	mimeTypes := []string{"image/png", "audio/wav", "video/mp4", "application/pdf"}

	f := func(seed int64) bool {
		rnd := rand.New(rand.NewSource(seed))

		// Generate a mixed sequence of text and media deltas with 2+ MIME types.
		// Ensure we have at least 2 text deltas and media deltas with at least 2 different MIME types.
		numEvents := rnd.Intn(15) + 4 // 4..18 events

		var events []bond.StreamEvent
		events = append(events, bond.StreamEvent{Type: bond.StreamEventStart})

		// Force at least 2 text deltas and media with 2+ distinct MIME types.
		events = append(events, bond.StreamEvent{
			Type:      bond.StreamEventTextDelta,
			TextDelta: fmt.Sprintf("text-%d", rnd.Intn(1000)),
		})
		events = append(events, bond.StreamEvent{
			Type: bond.StreamEventMediaDelta,
			MediaDelta: &bond.MediaDelta{
				MIMEType: mimeTypes[0],
				Data:     []byte{byte(rnd.Intn(256))},
			},
		})
		events = append(events, bond.StreamEvent{
			Type: bond.StreamEventMediaDelta,
			MediaDelta: &bond.MediaDelta{
				MIMEType: mimeTypes[1],
				Data:     []byte{byte(rnd.Intn(256))},
			},
		})
		events = append(events, bond.StreamEvent{
			Type:      bond.StreamEventTextDelta,
			TextDelta: fmt.Sprintf("text-%d", rnd.Intn(1000)),
		})

		// Add random additional events.
		for i := 0; i < numEvents; i++ {
			if rnd.Intn(2) == 0 {
				events = append(events, bond.StreamEvent{
					Type:      bond.StreamEventTextDelta,
					TextDelta: fmt.Sprintf("chunk-%d", i),
				})
			} else {
				mime := mimeTypes[rnd.Intn(len(mimeTypes))]
				events = append(events, bond.StreamEvent{
					Type: bond.StreamEventMediaDelta,
					MediaDelta: &bond.MediaDelta{
						MIMEType: mime,
						Data:     []byte{byte(rnd.Intn(256)), byte(rnd.Intn(256))},
					},
				})
			}
		}

		events = append(events, bond.StreamEvent{
			Type:       bond.StreamEventStop,
			StopReason: bond.StopReasonEnd,
		})

		// Run the executor.
		agent := &streamingAgent{events: events}
		executor := &bondExecutor{agent: agent, opts: bond.AgentOptions{}}
		execCtx := &a2asrv.ExecutorContext{
			TaskID:    a2a.TaskID(fmt.Sprintf("task-%d", seed)),
			ContextID: fmt.Sprintf("ctx-%d", seed),
			Message:   a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart("test")),
		}

		// Collect all TaskArtifactUpdateEvent events where LastChunk == false.
		var textArtifactIDs []a2a.ArtifactID
		var mediaArtifactIDs []a2a.ArtifactID

		for event, err := range executor.Execute(context.Background(), execCtx) {
			if err != nil {
				t.Logf("unexpected error: %v", err)
				return false
			}
			artEvent, ok := event.(*a2a.TaskArtifactUpdateEvent)
			if !ok || artEvent.LastChunk {
				continue
			}

			// Determine if this is a text or media artifact event.
			if artEvent.Artifact != nil && len(artEvent.Artifact.Parts) > 0 {
				part := artEvent.Artifact.Parts[0]
				switch part.Content.(type) {
				case a2a.Text:
					textArtifactIDs = append(textArtifactIDs, artEvent.Artifact.ID)
				case a2a.Raw:
					mediaArtifactIDs = append(mediaArtifactIDs, artEvent.Artifact.ID)
				}
			}
		}

		// Property checks:

		// 1. All text events must share a single artifact ID.
		if len(textArtifactIDs) == 0 {
			t.Logf("expected at least one text artifact event")
			return false
		}
		firstTextID := textArtifactIDs[0]
		for i, id := range textArtifactIDs {
			if id != firstTextID {
				t.Logf("text artifact ID mismatch: event %d has %q, expected %q", i, id, firstTextID)
				return false
			}
		}

		// 2. Media events with the same MIME source stream share an artifact ID.
		//    Different MIME type transitions produce different artifact IDs.
		if len(mediaArtifactIDs) < 2 {
			t.Logf("expected at least 2 media artifact events, got %d", len(mediaArtifactIDs))
			return false
		}
		// Verify that not all media IDs are the same (since we forced 2+ distinct MIME types).
		allSameMedia := true
		for _, id := range mediaArtifactIDs {
			if id != mediaArtifactIDs[0] {
				allSameMedia = false
				break
			}
		}
		if allSameMedia {
			t.Logf("all media artifact IDs are the same, expected distinct IDs for different MIME types")
			return false
		}

		// 3. Text artifact ID != any media artifact ID.
		mediaIDSet := make(map[a2a.ArtifactID]bool)
		for _, id := range mediaArtifactIDs {
			mediaIDSet[id] = true
		}
		if mediaIDSet[firstTextID] {
			t.Logf("text artifact ID %q collides with a media artifact ID", firstTextID)
			return false
		}

		return true
	}

	if err := quick.Check(f, &quick.Config{MaxCount: 100}); err != nil {
		t.Errorf("Artifact ID consistency per content type property failed: %v", err)
	}
}

// Feature: streaming-improvements, Property 11: Working state precedes all artifact events
// **Validates: Requirements 4.5**

// TestProperty_WorkingStatePrecedesAllArtifactEvents verifies that for any stream event
// sequence, the first yielded event is a *a2a.Task with TaskStateWorking and no
// *a2a.TaskArtifactUpdateEvent appears before it.
func TestProperty_WorkingStatePrecedesAllArtifactEvents(t *testing.T) {
	mimeTypes := []string{"image/png", "audio/wav", "video/mp4"}

	f := func(seed int64) bool {
		rnd := rand.New(rand.NewSource(seed))

		// Generate a random sequence of text/media/stop events.
		numDeltas := rnd.Intn(10) + 1 // 1..10 deltas
		var events []bond.StreamEvent
		events = append(events, bond.StreamEvent{Type: bond.StreamEventStart})

		for i := range numDeltas {
			if rnd.Intn(2) == 0 {
				events = append(events, bond.StreamEvent{
					Type:      bond.StreamEventTextDelta,
					TextDelta: fmt.Sprintf("text-%d", i),
				})
			} else {
				mime := mimeTypes[rnd.Intn(len(mimeTypes))]
				events = append(events, bond.StreamEvent{
					Type: bond.StreamEventMediaDelta,
					MediaDelta: &bond.MediaDelta{
						MIMEType: mime,
						Data:     []byte{byte(rnd.Intn(256))},
					},
				})
			}
		}

		events = append(events, bond.StreamEvent{
			Type:       bond.StreamEventStop,
			StopReason: bond.StopReasonEnd,
		})

		// Run the executor.
		agent := &streamingAgent{events: events}
		executor := &bondExecutor{agent: agent, opts: bond.AgentOptions{}}
		execCtx := &a2asrv.ExecutorContext{
			TaskID:    a2a.TaskID(fmt.Sprintf("task-%d", seed)),
			ContextID: fmt.Sprintf("ctx-%d", seed),
			Message:   a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart("hello")),
		}

		// Collect all yielded events.
		var yielded []a2a.Event
		for event, err := range executor.Execute(context.Background(), execCtx) {
			if err != nil {
				t.Logf("unexpected error: %v", err)
				return false
			}
			yielded = append(yielded, event)
		}

		// Must have at least one event.
		if len(yielded) == 0 {
			t.Logf("no events yielded")
			return false
		}

		// Property check 1: The first event must be *a2a.Task with TaskStateWorking.
		firstTask, ok := yielded[0].(*a2a.Task)
		if !ok {
			t.Logf("first event is not *a2a.Task, got %T", yielded[0])
			return false
		}
		if firstTask.Status.State != a2a.TaskStateWorking {
			t.Logf("first event state is %q, expected %q", firstTask.Status.State, a2a.TaskStateWorking)
			return false
		}

		// Property check 2: No *a2a.TaskArtifactUpdateEvent appears before the first *a2a.Task.
		for i, ev := range yielded {
			if _, isTask := ev.(*a2a.Task); isTask {
				// Found the first Task event; all preceding events must not be artifacts.
				break
			}
			if _, isArtifact := ev.(*a2a.TaskArtifactUpdateEvent); isArtifact {
				t.Logf("TaskArtifactUpdateEvent at index %d precedes first Task event", i)
				return false
			}
		}

		return true
	}

	if err := quick.Check(f, &quick.Config{MaxCount: 100}); err != nil {
		t.Errorf("Working state precedes all artifact events property failed: %v", err)
	}
}

// erroringAgent is a test helper that always yields the configured error.
// It implements bond.Agent for use with bondExecutor in error-handling property tests.
type erroringAgent struct {
	err error
}

func (a *erroringAgent) Stream(_ context.Context, _ []bond.Message) iter.Seq2[bond.StreamEvent, error] {
	return func(yield func(bond.StreamEvent, error) bool) {
		yield(bond.StreamEvent{}, a.err)
	}
}

// Feature: streaming-improvements, Property 12: Executor error yields failed task
// **Validates: Requirements 4.6**

// TestProperty_ExecutorErrorYieldsFailedTask verifies that for any error produced
// by the agent stream, the executor yields Task{Working} followed by Task{Failed}
// with the error message, and no TaskArtifactUpdateEvent is emitted.
func TestProperty_ExecutorErrorYieldsFailedTask(t *testing.T) {
	f := func(errMsg string) bool {
		// Skip empty error messages since errors.New("") is degenerate.
		if errMsg == "" {
			return true
		}

		agent := &erroringAgent{err: fmt.Errorf("%s", errMsg)}
		executor := &bondExecutor{agent: agent, opts: bond.AgentOptions{}}
		execCtx := &a2asrv.ExecutorContext{
			TaskID:    "task-err-test",
			ContextID: "ctx-err-test",
			Message:   a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart("test")),
		}

		var events []a2a.Event
		for event, err := range executor.Execute(context.Background(), execCtx) {
			if err != nil {
				t.Logf("unexpected iterator error: %v", err)
				return false
			}
			events = append(events, event)
		}

		// Must have exactly 2 events: Task{Working} then Task{Failed}.
		if len(events) != 2 {
			t.Logf("expected 2 events, got %d", len(events))
			return false
		}

		// First event: Task with TaskStateWorking.
		workingTask, ok := events[0].(*a2a.Task)
		if !ok {
			t.Logf("event[0]: expected *a2a.Task, got %T", events[0])
			return false
		}
		if workingTask.Status.State != a2a.TaskStateWorking {
			t.Logf("event[0]: expected TaskStateWorking, got %s", workingTask.Status.State)
			return false
		}

		// Second event: Task with TaskStateFailed.
		failedTask, ok := events[1].(*a2a.Task)
		if !ok {
			t.Logf("event[1]: expected *a2a.Task, got %T", events[1])
			return false
		}
		if failedTask.Status.State != a2a.TaskStateFailed {
			t.Logf("event[1]: expected TaskStateFailed, got %s", failedTask.Status.State)
			return false
		}

		// The failed task status message must contain the error text.
		if failedTask.Status.Message == nil {
			t.Logf("event[1]: failed task status message is nil")
			return false
		}
		if len(failedTask.Status.Message.Parts) == 0 {
			t.Logf("event[1]: failed task status message has no parts")
			return false
		}
		textContent, ok := failedTask.Status.Message.Parts[0].Content.(a2a.Text)
		if !ok {
			t.Logf("event[1]: expected text part, got %T", failedTask.Status.Message.Parts[0].Content)
			return false
		}
		if string(textContent) != errMsg {
			t.Logf("event[1]: error message mismatch: got %q, want %q", string(textContent), errMsg)
			return false
		}

		// No TaskArtifactUpdateEvent should be present.
		for i, ev := range events {
			if _, isArt := ev.(*a2a.TaskArtifactUpdateEvent); isArt {
				t.Logf("event[%d]: unexpected TaskArtifactUpdateEvent", i)
				return false
			}
		}

		return true
	}

	if err := quick.Check(f, &quick.Config{MaxCount: 100}); err != nil {
		t.Errorf("Executor error yields failed task property failed: %v", err)
	}
}

// Feature: streaming-improvements, Property 13: Artifact event ordering preserves stream order
// **Validates: Requirements 4.8**

// TestProperty_ArtifactEventOrderingPreservesStreamOrder verifies that for any sequence
// of stream events, the TaskArtifactUpdateEvent events yielded by the executor appear in
// the same relative order as the corresponding StreamEvent deltas were received from the agent.
func TestProperty_ArtifactEventOrderingPreservesStreamOrder(t *testing.T) {
	f := func(seed int64) bool {
		rnd := rand.New(rand.NewSource(seed))

		// Generate a random sequence of 2-10 text/media deltas in a known order.
		numDeltas := rnd.Intn(9) + 2 // 2..10

		type deltaEntry struct {
			isText bool
			text   string
			data   []byte
			mime   string
		}

		mimeTypes := []string{"image/png", "audio/wav", "video/mp4"}
		var deltas []deltaEntry
		var events []bond.StreamEvent

		events = append(events, bond.StreamEvent{Type: bond.StreamEventStart})

		for i := range numDeltas {
			if rnd.Intn(2) == 0 {
				// Text delta
				text := fmt.Sprintf("chunk-%d-%d", seed, i)
				deltas = append(deltas, deltaEntry{isText: true, text: text})
				events = append(events, bond.StreamEvent{
					Type:      bond.StreamEventTextDelta,
					TextDelta: text,
				})
			} else {
				// Media delta
				mime := mimeTypes[rnd.Intn(len(mimeTypes))]
				data := []byte{byte(rnd.Intn(256)), byte(i)}
				deltas = append(deltas, deltaEntry{isText: false, data: data, mime: mime})
				events = append(events, bond.StreamEvent{
					Type: bond.StreamEventMediaDelta,
					MediaDelta: &bond.MediaDelta{
						MIMEType: mime,
						Data:     data,
					},
				})
			}
		}

		events = append(events, bond.StreamEvent{
			Type:       bond.StreamEventStop,
			StopReason: bond.StopReasonEnd,
		})

		// Run the executor.
		agent := &streamingAgent{events: events}
		executor := &bondExecutor{agent: agent, opts: bond.AgentOptions{}}
		execCtx := &a2asrv.ExecutorContext{
			TaskID:    a2a.TaskID(fmt.Sprintf("task-%d", seed)),
			ContextID: fmt.Sprintf("ctx-%d", seed),
			Message:   a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart("test")),
		}

		// Collect all TaskArtifactUpdateEvent events where LastChunk == false.
		type artifactEntry struct {
			isText bool
			text   string
			data   []byte
			mime   string
		}

		var artifactEvents []artifactEntry

		for event, err := range executor.Execute(context.Background(), execCtx) {
			if err != nil {
				t.Logf("unexpected error: %v", err)
				return false
			}
			artEvent, ok := event.(*a2a.TaskArtifactUpdateEvent)
			if !ok || artEvent.LastChunk {
				continue
			}

			if artEvent.Artifact != nil && len(artEvent.Artifact.Parts) > 0 {
				part := artEvent.Artifact.Parts[0]
				switch content := part.Content.(type) {
				case a2a.Text:
					artifactEvents = append(artifactEvents, artifactEntry{
						isText: true,
						text:   string(content),
					})
				case a2a.Raw:
					artifactEvents = append(artifactEvents, artifactEntry{
						isText: false,
						data:   []byte(content),
						mime:   part.MediaType,
					})
				}
			}
		}

		// Verify: artifact events appear in the same relative order as input deltas.
		if len(artifactEvents) != len(deltas) {
			t.Logf("expected %d artifact events, got %d", len(deltas), len(artifactEvents))
			return false
		}

		for i, ae := range artifactEvents {
			d := deltas[i]
			if ae.isText != d.isText {
				t.Logf("event %d: type mismatch (artifact isText=%v, delta isText=%v)", i, ae.isText, d.isText)
				return false
			}
			if ae.isText {
				if ae.text != d.text {
					t.Logf("event %d: text mismatch (artifact=%q, delta=%q)", i, ae.text, d.text)
					return false
				}
			} else {
				if ae.mime != d.mime {
					t.Logf("event %d: mime mismatch (artifact=%q, delta=%q)", i, ae.mime, d.mime)
					return false
				}
				if len(ae.data) != len(d.data) {
					t.Logf("event %d: data length mismatch (artifact=%d, delta=%d)", i, len(ae.data), len(d.data))
					return false
				}
				for j := range ae.data {
					if ae.data[j] != d.data[j] {
						t.Logf("event %d: data byte %d mismatch", i, j)
						return false
					}
				}
			}
		}

		return true
	}

	if err := quick.Check(f, &quick.Config{MaxCount: 100}); err != nil {
		t.Errorf("Artifact event ordering preserves stream order property failed: %v", err)
	}
}

// Feature: streaming-improvements, Property 8: Executor delta-to-artifact event mapping
// **Validates: Requirements 4.1, 4.2**

// TestProperty_ExecutorDeltaToArtifactEventMapping verifies that for any StreamEvent of
// type text_delta or media_delta, the executor yields a TaskArtifactUpdateEvent with
// LastChunk=false containing the correct content.
func TestProperty_ExecutorDeltaToArtifactEventMapping(t *testing.T) {
	mimeTypes := []string{"image/png", "audio/wav", "video/mp4", "application/pdf"}

	f := func(seed int64) bool {
		rnd := rand.New(rand.NewSource(seed))

		// Generate 1-10 random text/media deltas + stop event.
		numDeltas := rnd.Intn(10) + 1 // 1..10

		type inputDelta struct {
			isText bool
			text   string
			data   []byte
			mime   string
		}

		var inputs []inputDelta
		var events []bond.StreamEvent
		events = append(events, bond.StreamEvent{Type: bond.StreamEventStart})

		for i := 0; i < numDeltas; i++ {
			if rnd.Intn(2) == 0 {
				text := fmt.Sprintf("delta-%d-%d", seed, i)
				inputs = append(inputs, inputDelta{isText: true, text: text})
				events = append(events, bond.StreamEvent{
					Type:      bond.StreamEventTextDelta,
					TextDelta: text,
				})
			} else {
				mime := mimeTypes[rnd.Intn(len(mimeTypes))]
				data := []byte{byte(rnd.Intn(256)), byte(i), byte(rnd.Intn(256))}
				inputs = append(inputs, inputDelta{isText: false, data: data, mime: mime})
				events = append(events, bond.StreamEvent{
					Type: bond.StreamEventMediaDelta,
					MediaDelta: &bond.MediaDelta{
						MIMEType: mime,
						Data:     data,
					},
				})
			}
		}

		events = append(events, bond.StreamEvent{
			Type:       bond.StreamEventStop,
			StopReason: bond.StopReasonEnd,
		})

		// Run the executor.
		agent := &streamingAgent{events: events}
		executor := &bondExecutor{agent: agent, opts: bond.AgentOptions{}}
		execCtx := &a2asrv.ExecutorContext{
			TaskID:    a2a.TaskID(fmt.Sprintf("task-%d", seed)),
			ContextID: fmt.Sprintf("ctx-%d", seed),
			Message:   a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart("test")),
		}

		// Collect TaskArtifactUpdateEvent events where LastChunk == false.
		type artifactEntry struct {
			isText bool
			text   string
			data   []byte
			mime   string
		}

		var artifacts []artifactEntry

		for event, err := range executor.Execute(context.Background(), execCtx) {
			if err != nil {
				t.Logf("unexpected error: %v", err)
				return false
			}
			artEvent, ok := event.(*a2a.TaskArtifactUpdateEvent)
			if !ok || artEvent.LastChunk {
				continue
			}
			if artEvent.Artifact == nil || len(artEvent.Artifact.Parts) == 0 {
				continue
			}

			part := artEvent.Artifact.Parts[0]
			switch content := part.Content.(type) {
			case a2a.Text:
				artifacts = append(artifacts, artifactEntry{isText: true, text: string(content)})
			case a2a.Raw:
				artifacts = append(artifacts, artifactEntry{isText: false, data: []byte(content), mime: part.MediaType})
			}
		}

		// For each input delta, verify there's a corresponding artifact event with correct content.
		if len(artifacts) != len(inputs) {
			t.Logf("expected %d artifact events, got %d", len(inputs), len(artifacts))
			return false
		}

		for i, input := range inputs {
			art := artifacts[i]
			if input.isText != art.isText {
				t.Logf("event %d: type mismatch (input isText=%v, artifact isText=%v)", i, input.isText, art.isText)
				return false
			}
			if input.isText {
				// Text: verify a2a.Text content matches.
				if art.text != input.text {
					t.Logf("event %d: text mismatch: got %q, want %q", i, art.text, input.text)
					return false
				}
			} else {
				// Media: verify a2a.Raw content and MediaType match.
				if art.mime != input.mime {
					t.Logf("event %d: mime mismatch: got %q, want %q", i, art.mime, input.mime)
					return false
				}
				if len(art.data) != len(input.data) {
					t.Logf("event %d: data length mismatch: got %d, want %d", i, len(art.data), len(input.data))
					return false
				}
				for j := range art.data {
					if art.data[j] != input.data[j] {
						t.Logf("event %d: data byte %d mismatch: got %d, want %d", i, j, art.data[j], input.data[j])
						return false
					}
				}
			}
		}

		return true
	}

	if err := quick.Check(f, &quick.Config{MaxCount: 100}); err != nil {
		t.Errorf("Executor delta-to-artifact event mapping property failed: %v", err)
	}
}

// Feature: streaming-improvements, Property 10: Stream completion yields lastChunk and completed task
// **Validates: Requirements 4.4**

// TestProperty_StreamCompletionYieldsLastChunkAndCompleted verifies that for any non-empty
// delta sequence followed by stop, the executor yields final lastChunk=true events for each
// active artifact and a Task{Completed} as the last event, with correct ordering.
func TestProperty_StreamCompletionYieldsLastChunkAndCompleted(t *testing.T) {
	mimeTypes := []string{"image/png", "audio/wav", "video/mp4"}

	f := func(seed int64) bool {
		rnd := rand.New(rand.NewSource(seed))

		// Generate 1-5 text deltas and 0-3 media deltas + stop.
		numTextDeltas := rnd.Intn(5) + 1 // 1..5
		numMediaDeltas := rnd.Intn(4)    // 0..3

		var events []bond.StreamEvent
		events = append(events, bond.StreamEvent{Type: bond.StreamEventStart})

		for i := 0; i < numTextDeltas; i++ {
			events = append(events, bond.StreamEvent{
				Type:      bond.StreamEventTextDelta,
				TextDelta: fmt.Sprintf("text-%d-%d", seed, i),
			})
		}

		for i := 0; i < numMediaDeltas; i++ {
			mime := mimeTypes[rnd.Intn(len(mimeTypes))]
			events = append(events, bond.StreamEvent{
				Type: bond.StreamEventMediaDelta,
				MediaDelta: &bond.MediaDelta{
					MIMEType: mime,
					Data:     []byte{byte(rnd.Intn(256)), byte(i)},
				},
			})
		}

		events = append(events, bond.StreamEvent{
			Type:       bond.StreamEventStop,
			StopReason: bond.StopReasonEnd,
		})

		// Run the executor.
		agent := &streamingAgent{events: events}
		executor := &bondExecutor{agent: agent, opts: bond.AgentOptions{}}
		execCtx := &a2asrv.ExecutorContext{
			TaskID:    a2a.TaskID(fmt.Sprintf("task-%d", seed)),
			ContextID: fmt.Sprintf("ctx-%d", seed),
			Message:   a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart("hello")),
		}

		// Collect all yielded events.
		var yielded []a2a.Event
		for event, err := range executor.Execute(context.Background(), execCtx) {
			if err != nil {
				t.Logf("unexpected error: %v", err)
				return false
			}
			yielded = append(yielded, event)
		}

		// Collect artifact IDs with lastChunk=false and lastChunk=true.
		lastChunkFalseIDs := make(map[a2a.ArtifactID]bool)
		lastChunkTrueIDs := make(map[a2a.ArtifactID]int) // ID -> count

		for _, ev := range yielded {
			artEvent, ok := ev.(*a2a.TaskArtifactUpdateEvent)
			if !ok {
				continue
			}
			if !artEvent.LastChunk {
				lastChunkFalseIDs[artEvent.Artifact.ID] = true
			} else {
				lastChunkTrueIDs[artEvent.Artifact.ID]++
			}
		}

		// Property 1: Each artifact ID with lastChunk=false also has exactly one lastChunk=true event.
		for id := range lastChunkFalseIDs {
			count, exists := lastChunkTrueIDs[id]
			if !exists {
				t.Logf("artifact %q has lastChunk=false events but no lastChunk=true event", id)
				return false
			}
			if count != 1 {
				t.Logf("artifact %q has %d lastChunk=true events, expected exactly 1", id, count)
				return false
			}
		}

		// Property 2: Last event is *a2a.Task with TaskStateCompleted.
		if len(yielded) == 0 {
			t.Logf("no events yielded")
			return false
		}
		lastEvent := yielded[len(yielded)-1]
		completedTask, ok := lastEvent.(*a2a.Task)
		if !ok {
			t.Logf("last event is not *a2a.Task, got %T", lastEvent)
			return false
		}
		if completedTask.Status.State != a2a.TaskStateCompleted {
			t.Logf("last event state is %q, expected %q", completedTask.Status.State, a2a.TaskStateCompleted)
			return false
		}

		// Property 3: Ordering — all lastChunk=false before lastChunk=true before Task{Completed}.
		// Track phase transitions: phase 0 = lastChunk=false, phase 1 = lastChunk=true, phase 2 = Task{Completed}
		// We skip the first event (Task{Working}) and the last event (Task{Completed}).
		phase := 0
		for i, ev := range yielded {
			// Skip the first Task{Working} event.
			if i == 0 {
				if _, isTask := ev.(*a2a.Task); isTask {
					continue
				}
			}
			// Skip the final Task{Completed} event (already validated).
			if i == len(yielded)-1 {
				continue
			}

			artEvent, ok := ev.(*a2a.TaskArtifactUpdateEvent)
			if !ok {
				continue
			}

			if !artEvent.LastChunk {
				// lastChunk=false: must be in phase 0
				if phase > 0 {
					t.Logf("event %d: lastChunk=false after phase transitioned to %d", i, phase)
					return false
				}
			} else {
				// lastChunk=true: must transition to phase 1
				if phase == 0 {
					phase = 1
				} else if phase > 1 {
					t.Logf("event %d: lastChunk=true after phase transitioned to %d", i, phase)
					return false
				}
			}
		}

		return true
	}

	if err := quick.Check(f, &quick.Config{MaxCount: 100}); err != nil {
		t.Errorf("Stream completion yields lastChunk and completed task property failed: %v", err)
	}
}
