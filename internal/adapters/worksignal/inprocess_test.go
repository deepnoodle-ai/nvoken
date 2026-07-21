package worksignal

import (
	"context"
	"testing"
	"time"
)

func TestInProcessScopesCoalescesAndClosesSubscriptions(t *testing.T) {
	signaller := NewInProcess()
	sub := signaller.Subscribe(context.Background(), []string{"invocations"})
	signaller.Notify(context.Background(), "other")
	if sub.Wait(context.Background(), time.Millisecond) {
		t.Fatal("subscription woke for another queue")
	}
	signaller.Notify(context.Background(), "invocations")
	signaller.Notify(context.Background(), "invocations")
	if !sub.Wait(context.Background(), time.Second) {
		t.Fatal("subscription did not wake")
	}
	if sub.Wait(context.Background(), time.Millisecond) {
		t.Fatal("duplicate notifications were not coalesced")
	}
	sub.Close()
	sub.Close()
	signaller.Notify(context.Background(), "invocations")
	if sub.Wait(context.Background(), time.Millisecond) {
		t.Fatal("closed subscription still woke")
	}
}
