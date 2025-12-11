package v1alpha1

import (
	"context"
	"fmt"

	governancev1alpha1 "github.com/AlwaysSayNo/quorum-based-manifests-governance/controller/api/v1alpha1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/validation/field"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

// +kubebuilder:webhook:path=/mrt/mutate,mutating=true,failurePolicy=fail,sideEffects=None,groups=governance.nazar.grynko.com,resources=manifestrequesttemplates,verbs=create,versions=v1alpha1,name=mrt-mutating-webhook.governance.nazar.grynko.com,admissionReviewVersions=v1
// +kubebuilder:webhook:path=/mrt/validate,mutating=false,failurePolicy=fail,sideEffects=None,groups=governance.nazar.grynko.com,resources=manifestrequesttemplates,verbs=create;update,versions=v1alpha1,name=mrt-validating-webhook.governance.nazar.grynko.com,admissionReviewVersions=v1

var mrtlog = logf.Log.WithName("mrt-resource")

func (w *ManifestRequestTemplateWebhook) SetupWebhookWithManager(mgr ctrl.Manager) error {
	w.Client = mgr.GetClient()

	return ctrl.NewWebhookManagedBy(mgr).
		For(&governancev1alpha1.ManifestRequestTemplate{}).
		WithDefaulter(w).
		WithValidator(w).
		Complete()
}

type ManifestRequestTemplateWebhook struct {
	Client client.Client
}

func (w *ManifestRequestTemplateWebhook) Default(ctx context.Context, obj runtime.Object) error {
	mrt, ok := obj.(*governancev1alpha1.ManifestRequestTemplate)
	if !ok {
		return fmt.Errorf("expected a ManifestRequestTemplate object but got %T", obj)
	}

	mrtlog.Info("defaulting MRT", "name", mrt.Name, "namespace", mrt.Namespace)

	// Set default fields on MRT namespace (if empty set 'default')
	if mrt.Namespace == "" {
		mrt.Namespace = "default"
	}

	// If on creation status.revisionQueue slice is nil -> set empty slice
	if mrt.Status.RevisionsQueue == nil {
		mrt.Status.RevisionsQueue = []string{}
	}

	return nil
}

func (w *ManifestRequestTemplateWebhook) ValidateCreate(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	mrt, ok := obj.(*governancev1alpha1.ManifestRequestTemplate)
	if !ok {
		return nil, fmt.Errorf("expected a ManifestRequestTemplate object but got %T", obj)
	}

	mrtlog.Info("validating MRT creation", "name", mrt.Name, "namespace", mrt.Namespace)
	var allErrs field.ErrorList

	// TODO: make reconciler to create resources on create event
	// TODO: make logic in repo to fetch MRT, even if it's in the governance folder

	// Validate, that argocd Application has the default namespace ('argocd')
	if mrt.Spec.ArgoCDApplication.Namespace != "argocd" {
		allErrs = append(allErrs, field.Invalid(field.NewPath("spec").Child("argoCDApplication").Child("namespace"), mrt.Spec.ArgoCDApplication.Namespace, "dynamic namespaces are not supported yet, must be 'argocd'"))
	}

	if len(allErrs) == 0 {
		return nil, nil
	}

	return nil, apierrors.NewInvalid(mrt.GroupVersionKind().GroupKind(), mrt.Name, allErrs)
}

func (w *ManifestRequestTemplateWebhook) ValidateUpdate(ctx context.Context, oldObj, newObj runtime.Object) (admission.Warnings, error) {
	oldMRT, _ := oldObj.(*governancev1alpha1.ManifestRequestTemplate)
	newMRT, _ := newObj.(*governancev1alpha1.ManifestRequestTemplate)

	mrtlog.Info("validating MRT update", "name", newMRT.Name, "namespace", newMRT.Namespace)
	var allErrs field.ErrorList

	// TODO: make reconciler to check existence of resources on actions

	// Validate, that on update MSR, MCA values cannot be changed
	if oldMRT.Spec.MSR != newMRT.Spec.MSR {
		allErrs = append(allErrs, field.Forbidden(field.NewPath("spec").Child("msr"), "MSR reference is immutable and cannot be changed after creation"))
	}
	if oldMRT.Spec.MCA != newMRT.Spec.MCA {
		allErrs = append(allErrs, field.Forbidden(field.NewPath("spec").Child("mca"), "MCA reference is immutable and cannot be changed after creation"))
	}

	if len(allErrs) == 0 {
		return nil, nil
	}

	return nil, apierrors.NewInvalid(newMRT.GroupVersionKind().GroupKind(), newMRT.Name, allErrs)
}

func (w *ManifestRequestTemplateWebhook) ValidateDelete(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	return nil, nil
}
