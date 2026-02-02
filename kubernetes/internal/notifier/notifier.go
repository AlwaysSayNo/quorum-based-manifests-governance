package notifier

import (
	"context"

	"sigs.k8s.io/controller-runtime/pkg/client"

	governancev1alpha1 "github.com/AlwaysSayNo/quorum-based-manifests-governance/kubernetes/api/v1alpha1"
)

type Notifier interface {
	NotifyGovernorsMSR(ctx context.Context, msr *governancev1alpha1.ManifestSigningRequest) error
	NotifyGovernorsMCA(ctx context.Context, mca *governancev1alpha1.ManifestChangeApproval) error
	NotifyError(ctx context.Context, channels []governancev1alpha1.NotificationChannel, message string) error

	// SupportsChannel returns true if the list contains at least one channel, that this notifier supports.
	SupportsChannel(channels []governancev1alpha1.NotificationChannel) bool
}

// NotifierFactory is an interface for creating notifier instances.
type NotifierFactory interface {
	// New creates a new notifier instance.
	New(k8sClient client.Client) Notifier
}
