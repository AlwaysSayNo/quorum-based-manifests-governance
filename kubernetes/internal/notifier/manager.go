package notifier

import (
	"context"
	"fmt"
	"sync"

	"github.com/go-logr/logr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	governancev1alpha1 "github.com/AlwaysSayNo/quorum-based-manifests-governance/kubernetes/api/v1alpha1"
)

// Manager is a registry and router for different notifier implementations.
// It maintains a list of notifier factories and routes notifications to appropriate notifiers.
type Manager struct {
	client    client.Client
	factories []NotifierFactory
	notifiers []Notifier
	mu        sync.RWMutex
	logger    logr.Logger
}

// NewManager creates a new notifier manager.
func NewManager(
	k8sClient client.Client,
) *Manager {
	return &Manager{
		client:    k8sClient,
		factories: []NotifierFactory{},
		notifiers: []Notifier{},
		logger:    log.FromContext(context.Background()).WithName("notifier-manager"),
	}
}

// Register registers a notifier factory with the manager.
func (m *Manager) Register(
	factory NotifierFactory,
) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.factories = append(m.factories, factory)
	// Create notifier instance
	notifier := factory.New(m.client)
	m.notifiers = append(m.notifiers, notifier)

	return nil
}

// NotifyGovernorsMSR sends MSR notifications through all registered notifiers.
func (m *Manager) NotifyGovernorsMSR(
	ctx context.Context,
	msr *governancev1alpha1.ManifestSigningRequest,
) error {
	if msr == nil {
		return fmt.Errorf("MSR cannot be nil")
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	logger := log.FromContext(ctx)
	var firstErr error

	for _, notifier := range m.notifiers {
		if !notifier.SupportsChannel(msr.Spec.Governors.NotificationChannels) {
			continue
		}

		if err := notifier.NotifyGovernorsMSR(ctx, msr); err != nil {
			logger.Error(err, "Failed to notify via MSR", "notifier", fmt.Sprintf("%T", notifier))
			if firstErr == nil {
				firstErr = err
			}
		}
	}

	return firstErr
}

// NotifyGovernorsMCA sends MCA notifications through all registered notifiers.
func (m *Manager) NotifyGovernorsMCA(
	ctx context.Context,
	mca *governancev1alpha1.ManifestChangeApproval,
) error {
	if mca == nil {
		return fmt.Errorf("MCA cannot be nil")
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	logger := log.FromContext(ctx)
	var firstErr error

	for _, notifier := range m.notifiers {
		if !notifier.SupportsChannel(mca.Spec.Governors.NotificationChannels) {
			continue
		}

		if err := notifier.NotifyGovernorsMCA(ctx, mca); err != nil {
			logger.Error(err, "Failed to notify via MCA", "notifier", fmt.Sprintf("%T", notifier))
			if firstErr == nil {
				firstErr = err
			}
		}
	}

	return firstErr
}

// NotifyError sends error notifications to all supported channels through registered notifiers.
func (m *Manager) NotifyError(
	ctx context.Context,
	mrt *governancev1alpha1.ManifestRequestTemplate,
	channels []governancev1alpha1.NotificationChannel,
	message string,
) error {
	if len(channels) == 0 {
		return nil
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	logger := log.FromContext(ctx)
	var firstErr error

	for _, notifier := range m.notifiers {
		if !notifier.SupportsChannel(channels) {
			continue
		}

		if err := notifier.NotifyError(ctx, mrt, channels, message); err != nil {
			logger.Error(err, "Failed to send error notification", "notifier", fmt.Sprintf("%T", notifier), "message", message)
			if firstErr == nil {
				firstErr = err
			}
		}
	}

	return firstErr
}
