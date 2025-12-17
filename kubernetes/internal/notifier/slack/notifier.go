package slack

import (
	"context"

	governancev1alpha1 "github.com/AlwaysSayNo/quorum-based-manifests-governance/kubernetes/api/v1alpha1"
)

type Notifier interface {
	NotifyGovernors(ctx context.Context, mrt *governancev1alpha1.ManifestRequestTemplate, msr *governancev1alpha1.ManifestSigningRequest) error
}

type slackNotifier struct {
}

// TODO: implement the method and maybe make the same architecture, as repository: manager+providers
func NewNotifier() *slackNotifier {
	return &slackNotifier{}
}

func (s *slackNotifier) NotifyGovernors(ctx context.Context, mrt *governancev1alpha1.ManifestRequestTemplate, msr *governancev1alpha1.ManifestSigningRequest) error {
	return nil
}
