package notifier

import (
	"context"

	governancev1alpha1 "github.com/AlwaysSayNo/quorum-based-manifests-governance/kubernetes/api/v1alpha1"
)

type Notifier interface {
	NotifyGovernorsMSR(ctx context.Context, msr *governancev1alpha1.ManifestSigningRequest) error
	NotifyGovernorsMCA(ctx context.Context, mca *governancev1alpha1.ManifestChangeApproval) error
}
