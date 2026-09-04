//go:build darwin || linux

package projectcheck

import (
	"context"
	"reflect"
	"runtime"
	"testing"
	"time"
)

func TestRunMakemigrationsConformanceFaultRejectsUnknownSelectorBeforeIO(t *testing.T) {
	report, err := RunMakemigrationsConformanceFault(MakemigrationsInvocation{}, MakemigrationsConformanceFault(255))
	if err == nil {
		t.Fatal("unknown makemigrations conformance fault was accepted")
	}
	if !reflect.DeepEqual(report, MakemigrationsReport{}) {
		t.Fatalf("invalid selector report = %+v, want zero", report)
	}
}

func TestRunMakemigrationsConformanceFinalCatalogRejectsNilBarrierBeforeIO(t *testing.T) {
	report, err := RunMakemigrationsConformanceFinalCatalog(MakemigrationsInvocation{}, nil)
	if err == nil {
		t.Fatal("nil final catalog barrier was accepted")
	}
	if !reflect.DeepEqual(report, MakemigrationsReport{}) {
		t.Fatalf("nil barrier report = %+v, want zero", report)
	}
}

func TestRunMakemigrationsConformanceFinalCatalogRejectsZeroBarrierBeforeIO(t *testing.T) {
	report, err := RunMakemigrationsConformanceFinalCatalog(
		MakemigrationsInvocation{},
		&MakemigrationsConformanceFinalCatalogBarrier{},
	)
	if err == nil {
		t.Fatal("zero final catalog barrier was accepted")
	}
	if !reflect.DeepEqual(report, MakemigrationsReport{}) {
		t.Fatalf("zero barrier report = %+v, want zero", report)
	}
}

func TestMakemigrationsConformanceFinalCatalogBarrierReleasesCanceledPeer(t *testing.T) {
	barrier := NewMakemigrationsConformanceFinalCatalogBarrier()
	parent, cancelParent := context.WithCancel(context.Background())
	firstContext, cancelFirst, err := barrier.claim(parent)
	if err != nil {
		t.Fatal(err)
	}
	defer cancelFirst()
	secondContext, cancelSecond, err := barrier.claim(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer cancelSecond()
	defer cancelParent()
	firstReturned := make(chan struct{})
	go func() {
		if barrier.arrive(firstContext, nil) {
			t.Error("canceled barrier reported a complete rendezvous")
		}
		close(firstReturned)
	}()
	deadline := time.Now().Add(time.Second)
	for barrier.arrivalCount() != 1 {
		if time.Now().After(deadline) {
			t.Fatal("first writer did not arrive at the final catalog barrier")
		}
		runtime.Gosched()
	}
	select {
	case <-firstReturned:
		t.Fatal("first writer crossed the barrier before its peer")
	default:
	}
	cancelParent()
	select {
	case <-firstReturned:
	case <-time.After(time.Second):
		t.Fatal("canceled writer did not release the final catalog barrier")
	}
	secondReturned := make(chan struct{})
	go func() {
		if barrier.arrive(secondContext, nil) {
			t.Error("peer of canceled barrier reported a complete rendezvous")
		}
		close(secondReturned)
	}()
	select {
	case <-secondReturned:
	case <-time.After(time.Second):
		t.Fatal("canceled writer left its peer blocked")
	}
}

func TestMakemigrationsConformanceFinalCatalogBarrierRejectsThirdClaim(t *testing.T) {
	barrier := NewMakemigrationsConformanceFinalCatalogBarrier()
	first, cancelFirst, err := barrier.claim(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer cancelFirst()
	second, cancelSecond, err := barrier.claim(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer cancelSecond()
	if _, cancelThird, err := barrier.claim(context.Background()); err == nil || cancelThird != nil {
		if cancelThird != nil {
			cancelThird()
		}
		t.Fatal("third final catalog barrier claim was accepted")
	}
	select {
	case <-first.Done():
	default:
		t.Fatal("third claim did not cancel the first writer")
	}
	select {
	case <-second.Done():
	default:
		t.Fatal("third claim did not cancel the second writer")
	}
}

func TestMakemigrationsConformanceFinalCatalogBarrierHasBoundedWait(t *testing.T) {
	barrier := newMakemigrationsConformanceFinalCatalogBarrier(10 * time.Millisecond)
	ctx, cancel, err := barrier.claim(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	returned := make(chan bool, 1)
	go func() { returned <- barrier.arrive(ctx, nil) }()
	select {
	case completed := <-returned:
		if completed {
			t.Fatal("one-writer timeout reported a complete rendezvous")
		}
	case <-time.After(time.Second):
		t.Fatal("final catalog barrier exceeded its bounded wait")
	}
}

func TestMakemigrationsConformanceFinalCatalogBarrierTimeoutPreservesCompletedRendezvous(t *testing.T) {
	barrier := NewMakemigrationsConformanceFinalCatalogBarrier()
	firstContext, cancelFirst, err := barrier.claim(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer cancelFirst()
	secondContext, cancelSecond, err := barrier.claim(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer cancelSecond()
	barrier.mu.Lock()
	barrier.arrivals = len(barrier.cancels)
	close(barrier.release)
	barrier.closed = true
	barrier.mu.Unlock()

	barrier.abortIfIncomplete()
	if !barrier.complete() {
		t.Fatal("timeout arbitration aborted an already completed final catalog rendezvous")
	}
	select {
	case <-firstContext.Done():
		t.Fatal("timeout arbitration canceled the first completed writer")
	default:
	}
	select {
	case <-secondContext.Done():
		t.Fatal("timeout arbitration canceled the second completed writer")
	default:
	}
}

func TestMakemigrationsConformanceFinalCatalogBarrierObservesInterrupt(t *testing.T) {
	barrier := NewMakemigrationsConformanceFinalCatalogBarrier()
	firstContext, cancelFirst, err := barrier.claim(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer cancelFirst()
	secondContext, cancelSecond, err := barrier.claim(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer cancelSecond()
	interrupt := make(chan struct{})
	returned := make(chan bool, 1)
	go func() { returned <- barrier.arrive(firstContext, interrupt) }()
	deadline := time.Now().Add(time.Second)
	for barrier.arrivalCount() != 1 {
		if time.Now().After(deadline) {
			t.Fatal("first writer did not arrive at the interruptible barrier")
		}
		runtime.Gosched()
	}
	close(interrupt)
	select {
	case completed := <-returned:
		if completed {
			t.Fatal("interrupted barrier reported a complete rendezvous")
		}
	case <-time.After(time.Second):
		t.Fatal("interrupted barrier did not return")
	}
	select {
	case <-secondContext.Done():
	default:
		t.Fatal("interrupt did not cancel the peer writer")
	}
}
