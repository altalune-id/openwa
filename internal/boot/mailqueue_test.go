package boot_test

import (
	"context"
	"slices"
	"testing"

	"altalune.id/openwa/internal/boot"
	"altalune.id/openwa/internal/platform/mailqueue"
)

func TestBootServer_MailQueueIsWired(t *testing.T) {
	srv, err := boot.BootServer(context.Background(), newSmokeCfg(t), boot.WithScheduler(false))
	if err != nil {
		t.Fatalf("BootServer: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close() })

	if srv.Platform.MailQueue == nil {
		t.Fatal("Kernel.MailQueue is nil; domains cannot queue mail")
	}
	var names []string
	for _, w := range srv.Supervisor.Workers() {
		names = append(names, w.Name())
	}
	if !slices.Contains(names, mailqueue.WorkerName) {
		t.Fatalf("supervisor workers = %v, want %q registered", names, mailqueue.WorkerName)
	}
}
