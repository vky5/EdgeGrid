package dashboard

import (
	"context"
	"errors"
	"maps"
	"strings"
	"testing"

	"github.com/edgegrid/edgegrid/internal/discovery"
)

// fakeTasks records what the Peers tab asked the node to do.
type fakeTasks struct {
	trust      map[string]bool
	dispatched []string // "peerID:kind"
	refuse     error
}

func (f *fakeTasks) funcs() TaskFuncs {
	return TaskFuncs{
		ListTrust: func() map[string]bool { return maps.Clone(f.trust) },
		SetTrust: func(id, _ string, allow bool) error {
			f.trust[id] = allow
			return nil
		},
		Dispatch: func(_ context.Context, p discovery.Peer, kind string) error {
			f.dispatched = append(f.dispatched, p.ID+":"+kind)
			return f.refuse
		},
	}
}

func modelWithTasks(f *fakeTasks, peers ...discovery.Peer) peersModel {
	m := modelWith(peers...)
	m.tasks = f.funcs()
	return m
}

func typeText(m peersModel, s string) peersModel {
	for _, r := range s {
		m, _ = m.updatePickKind(key(string(r)))
	}
	return m
}

// t toggles the task ACL only — file trust is a separate decision.
func TestTaskTrustToggleIsSeparateFromFileTrust(t *testing.T) {
	f := &fakeTasks{trust: map[string]bool{}}
	m := modelWithTasks(f, onlinePeer("stable-1", "alpha"))

	m, _ = m.updateBrowsing(key("t"))
	if !f.trust["stable-1"] {
		t.Fatal("t did not allow tasks from the selected peer")
	}
	if !strings.Contains(stripANSI(m.taskTrustMarker("stable-1")), "✓") {
		t.Error("task marker didn't update to allowed")
	}
	if !strings.Contains(stripANSI(m.trustMarker("stable-1")), "·") {
		t.Error("allowing tasks changed the file-trust marker")
	}

	m, _ = m.updateBrowsing(key("t"))
	if f.trust["stable-1"] {
		t.Error("second t did not block tasks again")
	}
}

func TestSendTaskOffersTheTypedKindToTheSelectedPeer(t *testing.T) {
	f := &fakeTasks{trust: map[string]bool{}}
	m := modelWithTasks(f, onlinePeer("a", "alpha"), onlinePeer("b", "bravo"))
	m.cursor = 1

	m, _ = m.updateBrowsing(key("x"))
	if m.mode != peersPickKind || !m.capturesTextInput() {
		t.Fatalf("x didn't open the kind prompt, mode = %v", m.mode)
	}
	m = typeText(m, "hello")
	m, cmd := m.updatePickKind(key("enter"))
	if m.mode != peersDispatching || cmd == nil {
		t.Fatalf("enter didn't dispatch, mode = %v", m.mode)
	}

	m, _ = m.Update(cmd())
	if len(f.dispatched) != 1 || f.dispatched[0] != "b:hello" {
		t.Errorf("dispatched = %v, want [b:hello]", f.dispatched)
	}
	if m.mode != peersBrowsing || !strings.Contains(m.sendNote, "accepted") {
		t.Errorf("mode = %v, note = %q, want back to browsing with an accepted note", m.mode, m.sendNote)
	}
}

func TestSendTaskShowsTheRefusal(t *testing.T) {
	f := &fakeTasks{trust: map[string]bool{}, refuse: errors.New("bravo: refused: this node cannot run task kind \"resnet\"")}
	m := modelWithTasks(f, onlinePeer("b", "bravo"))

	m, _ = m.updateBrowsing(key("x"))
	m = typeText(m, "resnet")
	m, cmd := m.updatePickKind(key("enter"))
	m, _ = m.Update(cmd())

	if m.flowErr == nil || !strings.Contains(m.flowErr.Error(), "resnet") {
		t.Errorf("flowErr = %v, want the refusal shown", m.flowErr)
	}
}

func TestSendTaskRefusesOfflinePeer(t *testing.T) {
	f := &fakeTasks{trust: map[string]bool{}}
	m := modelWithTasks(f, discovery.Peer{ID: "b", Hostname: "bravo", Online: false})

	m, _ = m.updateBrowsing(key("x"))
	if m.mode != peersBrowsing || m.flowErr == nil || !strings.Contains(m.flowErr.Error(), "offline") {
		t.Errorf("mode = %v, err = %v, want an offline error without entering the flow", m.mode, m.flowErr)
	}
}

// Dispatch can hang on a peer that never answers; esc must free the UI, and
// the answer that eventually arrives must not yank it back.
func TestEscWhileDispatchingDropsTheLateAnswer(t *testing.T) {
	f := &fakeTasks{trust: map[string]bool{}}
	m := modelWithTasks(f, onlinePeer("b", "bravo"))

	m, _ = m.updateBrowsing(key("x"))
	m = typeText(m, "hello")
	m, cmd := m.updatePickKind(key("enter"))
	m, _ = m.Update(key("esc"))
	if m.mode != peersBrowsing {
		t.Fatalf("esc didn't leave the dispatch wait, mode = %v", m.mode)
	}

	m, _ = m.Update(cmd())
	if m.sendNote != "" || m.flowErr != nil {
		t.Errorf("a late answer surfaced after esc: note=%q err=%v", m.sendNote, m.flowErr)
	}
}

func TestTaskKeysWithoutWiringShowAnError(t *testing.T) {
	m := modelWith(onlinePeer("b", "bravo"))

	m, _ = m.updateBrowsing(key("x"))
	if m.mode != peersBrowsing || m.flowErr == nil {
		t.Errorf("x with no dispatcher: mode = %v, err = %v", m.mode, m.flowErr)
	}
	m, _ = m.updateBrowsing(key("t"))
	if m.flowErr == nil {
		t.Error("t with no task ACL wired produced no error")
	}
}
