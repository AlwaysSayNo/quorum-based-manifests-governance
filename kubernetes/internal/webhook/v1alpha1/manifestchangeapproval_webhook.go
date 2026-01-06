/*
Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1alpha1

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"

	argoappv1 "github.com/argoproj/argo-cd/v3/pkg/apis/application/v1alpha1"
	"github.com/go-logr/logr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	unstructuredv1 "k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	governancev1alpha1 "github.com/AlwaysSayNo/quorum-based-manifests-governance/kubernetes/api/v1alpha1"
	governancecontroller "github.com/AlwaysSayNo/quorum-based-manifests-governance/kubernetes/internal/controller"
)

// +kubebuilder:webhook:path=/mca/validate/argocd-requests,mutating=false,failurePolicy=fail,sideEffects=None,groups=*,resources=*,verbs=create;update;delete,versions=*,name=mca-webhook.governance.nazar.grynko.com,admissionReviewVersions=v1,timeoutSeconds=30
// +kubebuilder:rbac:groups=governance.nazar.grynko.com,resources=governancequeues,verbs=get;list
// +kubebuilder:rbac:groups=governance.nazar.grynko.com,resources=governanceevents,verbs=get;list;watch;create;

func NewManifestChangeApprovalCustomValidator(
	client client.Client,
	decoder admission.Decoder,
) *ManifestChangeApprovalCustomValidator {
	return &ManifestChangeApprovalCustomValidator{
		Client:       client,
		Decoder:      decoder,
		initRevision: make(map[types.NamespacedName]string),
	}
}

type ManifestChangeApprovalCustomValidator struct {
	Client       client.Client
	Decoder      admission.Decoder
	initRevision map[types.NamespacedName]string
	logger logr.Logger
}

// Handle is the function that gets called for validation admission request.
func (v *ManifestChangeApprovalCustomValidator) Handle(
	ctx context.Context,
	req admission.Request,
) admission.Response {
	// if true {
	// 	return admission.Allowed("Change approved by Manifest Change Approval")
	// }

	v.logger = logf.FromContext(ctx).WithValues("controller", "AdmissionWebhook", "user", req.UserInfo)

	// Get Application resource for request
	application, resp, ok := v.getApplication(ctx, req)
	if !ok {
		return *resp
	}
	v.logger.WithValues("application", application.Name, "namespace", application.Namespace)

	// Get ManifestRequestTemplate resource linked to the current Application
	mrt, resp, ok := v.getMRTForApplication(ctx, application)
	if !ok {
		return *resp
	} else if mrt == nil {
		v.logger.Info("No governing MRT found for Application")
		return admission.Allowed("No governing MRT found, allow by default")
	}

	// Take revision commit hash from Application
	revision := v.getRevisionFromApplication(application)
	v.logger.Info("revision state", "revision", revision)

	mrtKey := types.NamespacedName{Name: mrt.Name, Namespace: mrt.Namespace}
	initRevision, exist := v.initRevision[mrtKey]
	if !controllerutil.ContainsFinalizer(mrt, governancecontroller.GovernanceFinalizer) && exist && initRevision != revision {
		v.logger.Info("Governing MRT not initialized yet, wait", "mrtName", mrt.Name, "mrtNamespace", mrt.Namespace, "revision", revision)
		return admission.Denied("Governing MRT not initialized yet, deny by default")
	} else if !controllerutil.ContainsFinalizer(mrt, governancecontroller.GovernanceFinalizer) && (!exist || initRevision == revision) {
		v.logger.Info("Governance initialization revision, allow", "revision", revision)
		return admission.Allowed("Governance initialization revision, allow by default")
	}
	delete(v.initRevision, mrtKey)

	// Get the latest ManifestChangeApproval for current ManifestRequestTemplate
	mca, resp, ok := v.getMCAForMRT(ctx, mrt)
	if !ok {
		return *resp
	} else if mca == nil {
		v.logger.Info("No MCA found for MRT", "mrt", mrt.Name, "namespace", mrt.Namespace)

		// If there is no ManifestChangeApproval -- add the revision to ManifestRequestTemplate review queue
		if resp, ok := v.appendRevisionToMRTCommitQueue(ctx, &revision, mrt); !ok {
			return *resp
		}
		return admission.Denied("No MCA found for MRT, deny by default")
	}

	// Check, if current revision was approved before (for it exists ManifestChangeApproval)
	requestedMCAIdx := slices.IndexFunc(mca.Status.ApprovalHistory, func(rec governancev1alpha1.ManifestChangeApprovalHistoryRecord) bool {
		return rec.CommitSHA == revision
	})
	if requestedMCAIdx == -1 {
		v.logger.V(3).Info("No approval found in MCA for requested revision", "revision", revision, "mca", mca.Name)

		// If there is no ManifestChangeApproval, corresponding for this revision -- put it into ManifestRequestTemplate review queue
		if resp, ok := v.appendRevisionToMRTCommitQueue(ctx, &revision, mrt); !ok {
			return *resp
		}

		return admission.Denied(fmt.Sprintf("Change from commit %s has not been approved by the governance board.", revision))
	}

	// Check, if revision corresponds to the latest ManifestChangeApproval
	// If not, it might be rollback request and it should be handled differently
	mcaRecord := mca.Status.ApprovalHistory[requestedMCAIdx]
	if mcaRecord.CommitSHA != mca.Spec.CommitSHA {
		v.logger.Info("Application revision is older than last approved commit in MCA", "requestedRevision", revision, "lastApprovedCommitSHA", mca.Spec.CommitSHA)
		// TODO: might be rollback, and should be handled differently. Deny for now
		return admission.Denied(fmt.Sprintf("Change from commit %s is older than last approved commit %s in MCA.", revision, mca.Spec.CommitSHA))
	}

	// Otherwise, the request is latest approved and we allow, to apply it
	v.logger.Info("Allowing Argo CD Application request approved by MCA", "commit", revision)
	return admission.Allowed("Change approved by Manifest Change Approval")
}

func (v *ManifestChangeApprovalCustomValidator) getApplication(
	ctx context.Context,
	req admission.Request,
) (*argoappv1.Application, *admission.Response, bool) {
	if req.Kind.Kind != "Application" {
		return v.getApplicationFromNonApplicationRequest(ctx, req)
	} else {
		return v.getApplicationFromApplicationRequest(req)
	}
}

func (v *ManifestChangeApprovalCustomValidator) getApplicationFromNonApplicationRequest(
	ctx context.Context,
	req admission.Request,
) (*argoappv1.Application, *admission.Response, bool) {
	gvk := v.getGroupVersionKind(req)

	unstruct := &unstructuredv1.Unstructured{}
	unstruct.SetGroupVersionKind(gvk)
	if err := v.Decoder.Decode(req, unstruct); err != nil {
		v.logger.Error(err, "Failed decoding resource from ArgoCD request", "resource", gvk.Kind, "name", req.Name, "namespace", req.Namespace)
		resp := admission.Errored(http.StatusBadRequest, err)
		return nil, &resp, false
	}

	trackingID := unstruct.GetAnnotations()["argocd.argoproj.io/tracking-id"]
	if trackingID == "" {
		v.logger.Info("ArgoCD tracking-id not found; denying by default to avoid unapproved applies", "resource", gvk.Kind, "name", req.Name, "namespace", req.Namespace)
		resp := admission.Denied("ArgoCD tracking-id not found; unable to verify approval for this resource")
		return nil, &resp, false
	}

	appName, appNamespace, ok := parseTrackingID(trackingID)
	if !ok {
		v.logger.Info("Failed to parse ArgoCD tracking-id; cannot map to Application", "trackingID", trackingID)
		resp := admission.Errored(http.StatusBadRequest, fmt.Errorf("invalid tracking-id format: %s", trackingID))
		return nil, &resp, false
	}

	// Fetch the ArgoCD Application
	application := &argoappv1.Application{}
	appKey := types.NamespacedName{Name: appName, Namespace: appNamespace}
	if err := v.Client.Get(ctx, appKey, application); err != nil {
		v.logger.Error(err, "Failed to fetch ArgoCD Application for tracking-id", "name", appName, "namespace", appNamespace)
		resp := admission.Errored(http.StatusInternalServerError, fmt.Errorf("get App %s.%s: %w", appNamespace, appName, err))
		return nil, &resp, false
	}

	return application, nil, true
}

func (v *ManifestChangeApprovalCustomValidator) getApplicationFromApplicationRequest(
	req admission.Request,
) (*argoappv1.Application, *admission.Response, bool) {
	gvk := v.getGroupVersionKind(req)

	application := &argoappv1.Application{}
	application.SetGroupVersionKind(gvk)
	if err := v.Decoder.Decode(req, application); err != nil {
		v.logger.Error(err, "Failed decoding resource from ArgoCD request", "resource", gvk.Kind, "name", req.Name, "namespace", req.Namespace)
		resp := admission.Errored(http.StatusBadRequest, err)
		return nil, &resp, false
	}

	return application, nil, true
}

func (v *ManifestChangeApprovalCustomValidator) getMRTForApplication(
	ctx context.Context,
	applicationObj *argoappv1.Application,
) (*governancev1alpha1.ManifestRequestTemplate, *admission.Response, bool) {
	// Fetch list of all MRTs
	mrtList := &governancev1alpha1.ManifestRequestTemplateList{}
	if err := v.Client.List(ctx, mrtList); err != nil {
		v.logger.Error(err, "Failed to get ManifestRequestTemplates list while getting MRT")
		resp := admission.Errored(http.StatusInternalServerError, fmt.Errorf("list ManifestRequestTemplates: %w", err))
		return nil, &resp, false
	}

	for _, mrtItem := range mrtList.Items {
		// Check if this MRT governs this Application
		mrtName := mrtItem.Spec.ArgoCDApplication.Name
		mrtNamespace := mrtItem.Spec.ArgoCDApplication.Namespace
		if mrtName != applicationObj.Name || mrtNamespace != applicationObj.Namespace {
			continue
		}

		return &mrtItem, nil, true
	}

	// No MRT found
	return nil, nil, true
}

func (v *ManifestChangeApprovalCustomValidator) getMCAForMRT(
	ctx context.Context,
	mrt *governancev1alpha1.ManifestRequestTemplate,
) (*governancev1alpha1.ManifestChangeApproval, *admission.Response, bool) {
	// Fetch list of all MRTs
	mcaList := &governancev1alpha1.ManifestChangeApprovalList{}
	if err := v.Client.List(ctx, mcaList); err != nil {
		v.logger.Error(err, "Failed to get ManifestChangeApproval list while getting MCA")
		resp := admission.Errored(http.StatusInternalServerError, fmt.Errorf("list ManifestChangeApproval: %w", err))
		return nil, &resp, false
	}

	// Find MCA for this revision
	for _, mcaItem := range mcaList.Items {
		if mcaItem.Name == mrt.Spec.MCA.Name && mcaItem.Namespace == mrt.Spec.MCA.Namespace {
			return &mcaItem, nil, true
		}
	}

	// No MCA found
	return nil, nil, true
}

func (v *ManifestChangeApprovalCustomValidator) getRevisionFromApplication(
	applicationObj *argoappv1.Application,
) string {
	// TODO: extra checks when do rollback or sync to previous revision
	revision := ""
	if applicationObj.Status.OperationState != nil && applicationObj.Status.OperationState.Operation.Sync != nil {
		// revision is stored in SyncResult (for ongoing or finished syncs)
		revision = applicationObj.Status.OperationState.Operation.Sync.Revision
	} else if applicationObj.Operation != nil && applicationObj.Operation.Sync != nil {
		// revision coming from argocd-server
		revision = applicationObj.Operation.Sync.Revision
	}

	return revision
}

func (v *ManifestChangeApprovalCustomValidator) appendRevisionToMRTCommitQueue(
	ctx context.Context,
	revision *string,
	mrt *governancev1alpha1.ManifestRequestTemplate,
) (*admission.Response, bool) {
	queueRef := mrt.Status.RevisionQueueRef

	contains, err := v.queueContainsRevision(ctx, revision, mrt)
	if err != nil {
		v.logger.Error(err, "Failed to check, if queue contains revision", "queue", queueRef, "revision", revision)
		resp := admission.Errored(http.StatusInternalServerError, fmt.Errorf("check, if queue %v contains revision %s: %w", queueRef, *revision, err))
		return &resp, false
	}
	if !contains {
		if err := v.createRevisionEvent(ctx, revision, mrt); err != nil {
			v.logger.Error(err, "Failed to update MRT status with new commit in queue", "mrt", mrt.Name, "namespace", mrt.Namespace, "revision", revision)
			resp := admission.Errored(http.StatusInternalServerError, fmt.Errorf("update MRT %s.%s status with new commit in queue: %w", mrt.Namespace, mrt.Name, err))
			return &resp, false
		}
		v.logger.Info("Added revision to MRT commits queue for approval processing", "mrt", mrt.Name, "namespace", mrt.Namespace, "revision", revision)
	}

	return nil, true
}

func (v *ManifestChangeApprovalCustomValidator) queueContainsRevision(
	ctx context.Context,
	revision *string,
	mrt *governancev1alpha1.ManifestRequestTemplate,
) (bool, error) {
	events, err := v.getAllEventsForQueue(ctx, mrt)
	if err != nil {
		return false, fmt.Errorf("get all events for queue: %w", err)
	}

	eventIdx := slices.IndexFunc(events, func(e governancev1alpha1.GovernanceEvent) bool {
		return e.Spec.NewRevision != nil && e.Spec.NewRevision.CommitSHA == *revision
	})
	return eventIdx != -1, nil
}

func (v *ManifestChangeApprovalCustomValidator) getAllEventsForQueue(
	ctx context.Context,
	mrt *governancev1alpha1.ManifestRequestTemplate,
) ([]governancev1alpha1.GovernanceEvent, error) {
	eventList := &governancev1alpha1.GovernanceEventList{}

	// Use label for quick search
	matchingLabels := client.MatchingLabels{
		"governance.nazar.grynko.com/mrt-uid": string(mrt.UID),
	}

	if err := v.Client.List(ctx, eventList, matchingLabels); err != nil {
		v.logger.Error(err, "Failed to list GovernanceEvents for queue")
		return nil, fmt.Errorf("list GovernanceEvents for queue: %w", err)
	}
	return eventList.Items, nil
}

func (v *ManifestChangeApprovalCustomValidator) createRevisionEvent(
	ctx context.Context,
	revision *string,
	mrt *governancev1alpha1.ManifestRequestTemplate,
) error {
	// Create a stable, unique name.
	eventName := fmt.Sprintf("event-%s-%s-%s", mrt.Name, mrt.Namespace, *revision)

	revisionEvent := governancev1alpha1.GovernanceEvent{
		ObjectMeta: metav1.ObjectMeta{
			Name:      eventName,
			Namespace: mrt.Namespace,
			Labels: map[string]string{
				"governance.nazar.grynko.com/mrt-uid": string(mrt.UID), // Use UID for a unique, immutable link
			},
		},
		Spec: governancev1alpha1.GovernanceEventSpec{
			Type: governancev1alpha1.EventTypeNewRevision,
			MRT: governancev1alpha1.ManifestRef{
				Name:      mrt.Name,
				Namespace: mrt.Namespace,
			},
			NewRevision: &governancev1alpha1.NewRevisionPayload{
				CommitSHA: *revision,
			},
		},
	}

	if err := v.Client.Create(ctx, &revisionEvent); err != nil {
		return fmt.Errorf("create new revision %s event: %w", *revision, err)
	}

	return nil
}

func (v *ManifestChangeApprovalCustomValidator) getGroupVersionKind(
	req admission.Request,
) schema.GroupVersionKind {
	return schema.GroupVersionKind{
		Group:   req.Kind.Group,
		Version: req.Kind.Version,
		Kind:    req.Kind.Kind,
	}
}

// InjectDecoder injects the decoder.
func (v *ManifestChangeApprovalCustomValidator) InjectDecoder(
	d admission.Decoder,
) error {
	v.Decoder = d
	return nil
}

// Helper: parse tracking-id into appName and appNamespace
func parseTrackingID(
	trackingID string,
) (string, string, bool) {
	// Expected: "<application-name>:<group>/<kind>:<namespace>/<name>"
	parts := strings.SplitN(trackingID, ":", 3)
	if len(parts) < 3 {
		return "", "", false
	}
	appName := parts[0]
	// TODO: we suppose, that all Applications are stored inside the default for ArgoCD namespace -- argocd.
	// But ArgoCD has functionality since version 2.5, which allows to deploy Application resources in other namespace.
	// To handle this, extra logic is required.
	appNamespace := "argocd"
	return appName, appNamespace, true
}
