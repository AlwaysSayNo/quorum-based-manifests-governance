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
	logger       logr.Logger
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

	// Extract the Application resource
	application, resp := v.getApplication(ctx, req)
	if resp != nil {
		return *resp
	}
	v.logger.WithValues("appName", application.Name, "appNamespace", application.Namespace)

	// Find the governing MRT
	mrt, resp := v.validateAndGetMRT(ctx, application)
	if resp != nil {
		return *resp
	}

	// Check if MRT is initialized
	revision := v.getRevisionFromApplication(application)
	v.logger.WithValues("mrtName", mrt.Name, "mrtNamespace", mrt.Namespace, "requestedRevision", revision)

	if resp := v.validateMRTInitialization(mrt, revision); resp != nil {
		return *resp
	}

	// Validate the revision is approved
	return v.validateRevisionApproval(ctx, mrt, revision)
}

// validateAndGetMRT retrieves the MRT governing the Application
func (v *ManifestChangeApprovalCustomValidator) validateAndGetMRT(
	ctx context.Context,
	application *argoappv1.Application,
) (*governancev1alpha1.ManifestRequestTemplate, *admission.Response) {
	mrt, resp := v.getMRTForApplication(ctx, application)
	if resp != nil {
		return nil, resp
	}

	if mrt == nil {
		v.logger.Info("No governing MRT found for Application")
		resp := admission.Allowed("No governing MRT found, allow by default")
		return nil, &resp
	}

	return mrt, nil
}

// validateMRTInitialization checks if the MRT has been initialized
func (v *ManifestChangeApprovalCustomValidator) validateMRTInitialization(
	mrt *governancev1alpha1.ManifestRequestTemplate,
	revision string,
) *admission.Response {
	mrtKey := types.NamespacedName{Name: mrt.Name, Namespace: mrt.Namespace}
	initRevision, exist := v.initRevision[mrtKey]

	// MRT not yet initialized (no finalizer)
	if !controllerutil.ContainsFinalizer(mrt, governancecontroller.GovernanceFinalizer) {
		// Revision doesn't correspond to the initialization revision (newer revision)
		if exist && initRevision != revision {
			v.logger.Info("Governing MRT not initialized yet, wait")
			resp := admission.Denied("Governing MRT not initialized yet, deny by default")
			return &resp
		}

		// Initialization revision. Allow, to avoid infinite error loop
		if !exist || initRevision == revision {
			v.logger.Info("Governance initialization revision, allow")
			resp := admission.Allowed("Governance initialization revision, allow by default")
			return &resp
		}
	}

	delete(v.initRevision, mrtKey)
	return nil
}

// validateRevisionApproval checks if the revision has been approved by MCA
func (v *ManifestChangeApprovalCustomValidator) validateRevisionApproval(
	ctx context.Context,
	mrt *governancev1alpha1.ManifestRequestTemplate,
	revision string,
) admission.Response {
	// Get the latest MCA for this MRT
	mca, resp := v.getMCAForMRT(ctx, mrt)
	if resp != nil {
		return *resp
	}

	// No MCA found - add to review queue
	if mca == nil {
		v.logger.Info("No MCA found for MRT")

		if resp, ok := v.appendRevisionToMRTCommitQueue(ctx, &revision, mrt); !ok {
			return *resp
		}
		return admission.Denied("No MCA found for MRT, deny by default")
	}
	v.logger.WithValues("mcaName", mca.Name, "mcaNamespace", mca.Namespace)

	// Check if revision is in approval history
	return v.checkRevisionApprovalStatus(ctx, revision, mca, mrt)
}

// checkRevisionApprovalStatus validates if the requested revision is approved
func (v *ManifestChangeApprovalCustomValidator) checkRevisionApprovalStatus(
	ctx context.Context,
	revision string,
	mca *governancev1alpha1.ManifestChangeApproval,
	mrt *governancev1alpha1.ManifestRequestTemplate,
) admission.Response {
	// Find the revision in approval history
	requestedMCAIdx := slices.IndexFunc(mca.Status.ApprovalHistory, func(rec governancev1alpha1.ManifestChangeApprovalHistoryRecord) bool {
		return rec.CommitSHA == revision
	})

	// Revision not found in approval history
	if requestedMCAIdx == -1 {
		v.logger.V(3).Info("No approval found in MCA for requested revision")

		if resp, ok := v.appendRevisionToMRTCommitQueue(ctx, &revision, mrt); !ok {
			return *resp
		}

		return admission.Denied(fmt.Sprintf("Change from commit %s has not been approved by the governance board.", revision))
	}

	// Check if it's the latest approved revision
	mcaRecord := mca.Status.ApprovalHistory[requestedMCAIdx]
	if mcaRecord.CommitSHA != mca.Spec.CommitSHA {
		v.logger.Info("Application revision is older than last approved commit in MCA", "lastApprovedCommitSHA", mca.Spec.CommitSHA)
		return admission.Denied(fmt.Sprintf("Change from commit %s is older than last approved commit %s in MCA.", revision, mca.Spec.CommitSHA))
	}

	// Revision is approved and latest - allow it
	v.logger.Info("Application request is approved by the latest MCA. Allow", "lastApprovedCommitSHA", mca.Spec.CommitSHA)
	return admission.Allowed("Change approved by Manifest Change Approval")
}

func (v *ManifestChangeApprovalCustomValidator) getApplication(
	ctx context.Context,
	req admission.Request,
) (*argoappv1.Application, *admission.Response) {
	if req.Kind.Kind != "Application" {
		return v.getApplicationFromNonApplicationRequest(ctx, req)
	} else {
		return v.getApplicationFromApplicationRequest(req)
	}
}

func (v *ManifestChangeApprovalCustomValidator) getApplicationFromNonApplicationRequest(
	ctx context.Context,
	req admission.Request,
) (*argoappv1.Application, *admission.Response) {
	gvk := v.getGroupVersionKind(req)

	unstruct := &unstructuredv1.Unstructured{}
	unstruct.SetGroupVersionKind(gvk)
	if err := v.Decoder.Decode(req, unstruct); err != nil {
		v.logger.Error(err, "Failed decoding resource from ArgoCD request", "resource", gvk.Kind, "name", req.Name, "namespace", req.Namespace)
		resp := admission.Errored(http.StatusBadRequest, err)
		return nil, &resp
	}

	trackingID := unstruct.GetAnnotations()["argocd.argoproj.io/tracking-id"]
	if trackingID == "" {
		v.logger.Info("ArgoCD tracking-id not found; denying by default to avoid unapproved applies", "resource", gvk.Kind, "name", req.Name, "namespace", req.Namespace)
		resp := admission.Denied("ArgoCD tracking-id not found; unable to verify approval for this resource")
		return nil, &resp
	}

	appName, appNamespace, ok := parseTrackingID(trackingID)
	if !ok {
		v.logger.Info("Failed to parse ArgoCD tracking-id; cannot map to Application", "trackingID", trackingID)
		resp := admission.Errored(http.StatusBadRequest, fmt.Errorf("invalid tracking-id format: %s", trackingID))
		return nil, &resp
	}

	// Fetch the ArgoCD Application
	application := &argoappv1.Application{}
	appKey := types.NamespacedName{Name: appName, Namespace: appNamespace}
	if err := v.Client.Get(ctx, appKey, application); err != nil {
		v.logger.Error(err, "Failed to fetch ArgoCD Application for tracking-id", "name", appName, "namespace", appNamespace)
		resp := admission.Errored(http.StatusInternalServerError, fmt.Errorf("get App %s.%s: %w", appNamespace, appName, err))
		return nil, &resp
	}

	return application, nil
}

func (v *ManifestChangeApprovalCustomValidator) getApplicationFromApplicationRequest(
	req admission.Request,
) (*argoappv1.Application, *admission.Response) {
	gvk := v.getGroupVersionKind(req)

	application := &argoappv1.Application{}
	application.SetGroupVersionKind(gvk)
	if err := v.Decoder.Decode(req, application); err != nil {
		v.logger.Error(err, "Failed decoding resource from ArgoCD request", "resource", gvk.Kind, "name", req.Name, "namespace", req.Namespace)
		resp := admission.Errored(http.StatusBadRequest, err)
		return nil, &resp
	}

	return application, nil
}

func (v *ManifestChangeApprovalCustomValidator) getMRTForApplication(
	ctx context.Context,
	applicationObj *argoappv1.Application,
) (*governancev1alpha1.ManifestRequestTemplate, *admission.Response) {
	// Fetch list of all MRTs
	mrtList := &governancev1alpha1.ManifestRequestTemplateList{}
	if err := v.Client.List(ctx, mrtList); err != nil {
		v.logger.Error(err, "Failed to get ManifestRequestTemplates list while getting MRT")
		resp := admission.Errored(http.StatusInternalServerError, fmt.Errorf("list ManifestRequestTemplates: %w", err))
		return nil, &resp
	}

	for _, mrtItem := range mrtList.Items {
		// Check if this MRT governs this Application
		mrtName := mrtItem.Spec.ArgoCDApplication.Name
		mrtNamespace := mrtItem.Spec.ArgoCDApplication.Namespace
		if mrtName != applicationObj.Name || mrtNamespace != applicationObj.Namespace {
			continue
		}

		return &mrtItem, nil
	}

	// No MRT found
	return nil, nil
}

func (v *ManifestChangeApprovalCustomValidator) getMCAForMRT(
	ctx context.Context,
	mrt *governancev1alpha1.ManifestRequestTemplate,
) (*governancev1alpha1.ManifestChangeApproval, *admission.Response) {
	// Fetch list of all MRTs
	mcaList := &governancev1alpha1.ManifestChangeApprovalList{}
	if err := v.Client.List(ctx, mcaList); err != nil {
		v.logger.Error(err, "Failed to get ManifestChangeApproval list while getting MCA")
		resp := admission.Errored(http.StatusInternalServerError, fmt.Errorf("list ManifestChangeApproval: %w", err))
		return nil, &resp
	}

	// Find MCA for this revision
	for _, mcaItem := range mcaList.Items {
		if mcaItem.Name == mrt.Spec.MCA.Name && mcaItem.Namespace == mrt.Spec.MCA.Namespace {
			return &mcaItem, nil
		}
	}

	// No MCA found
	return nil, nil
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
		v.logger.Error(err, "Failed to check, if queue contains revision", "queue", queueRef)
		resp := admission.Errored(http.StatusInternalServerError, fmt.Errorf("check, if queue %v contains revision %s: %w", queueRef, *revision, err))
		return &resp, false
	}
	if !contains {
		if err := v.createRevisionEvent(ctx, revision, mrt); err != nil {
			v.logger.Error(err, "Failed to update MRT status with new commit in queue")
			resp := admission.Errored(http.StatusInternalServerError, fmt.Errorf("update MRT %s.%s status with new commit in queue: %w", mrt.Namespace, mrt.Name, err))
			return &resp, false
		}
		v.logger.Info("Added revision to MRT commits queue for approval processing")
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
