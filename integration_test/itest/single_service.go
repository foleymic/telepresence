package itest

import (
	"context"
	"fmt"
	"os"
	"testing"
)

type SingleService interface {
	NamespacePair
	ServiceName() string
}

type singleService struct {
	NamespacePair
	serviceName string
}

func WithSingleService(h NamespacePair, serviceName string, f func(SingleService)) {
	h.HarnessT().Run(fmt.Sprintf("Test_Service_%s", serviceName), func(t *testing.T) {
		ctx := WithT(h.HarnessContext(), t)
		s := &singleService{NamespacePair: h, serviceName: serviceName}
		s.PushHarness(ctx, s.setup, s.tearDown)
		defer h.PopHarness()
		f(s)
	})
}

func (h *singleService) setup(ctx context.Context) bool {
	h.ApplyEchoService(ctx, h.serviceName, 80)
	return true
}

func (h *singleService) tearDown(ctx context.Context) {
	if os.Getenv("TELEPRESENCE_TEST_SKIP_TEARDOWN") != "" {
		return
	}
	h.DeleteSvcAndWorkload(ctx, "deploy", h.serviceName)
}

func (h *singleService) ServiceName() string {
	return h.serviceName
}
