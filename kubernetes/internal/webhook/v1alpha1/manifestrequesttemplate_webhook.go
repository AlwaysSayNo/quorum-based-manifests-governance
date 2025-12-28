package v1alpha1

import (
	"context"
	"fmt"

	governancev1alpha1 "github.com/AlwaysSayNo/quorum-based-manifests-governance/kubernetes/api/v1alpha1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/validation/field"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

// +kubebuilder:webhook:path=/mrt/mutate,mutating=true,failurePolicy=fail,sideEffects=None,groups=governance.nazar.grynko.com,resources=manifestrequesttemplates,verbs=create,versions=v1alpha1,name=mrt-mutating-webhook.governance.nazar.grynko.com,admissionReviewVersions=v1,timeoutSeconds=30
// +kubebuilder:webhook:path=/mrt/validate,mutating=false,failurePolicy=fail,sideEffects=None,groups=governance.nazar.grynko.com,resources=manifestrequesttemplates,verbs=create;update,versions=v1alpha1,name=mrt-validating-webhook.governance.nazar.grynko.com,admissionReviewVersions=v1,timeoutSeconds=30

var mrtlog = logf.Log.WithName("mrt-resource")

const (
	MSRDefaultName         = "manifestsigningrequest"
	MCADefaultName         = "manifestchangeapproval"
	ArgoCDDefaultNamespace = "argocd"
	LocationDefaultFolder  = "qubmango"
)

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

	// Set default MSR values
	if mrt.Spec.MSR.Name == "" {
		mrt.Spec.MSR.Name = MSRDefaultName
	}
	if mrt.Spec.MSR.Namespace == "" {
		mrt.Spec.MSR.Namespace = mrt.Namespace
	}

	// Set default MCA values
	if mrt.Spec.MCA.Name == "" {
		mrt.Spec.MCA.Name = MCADefaultName
	}
	if mrt.Spec.MCA.Namespace == "" {
		mrt.Spec.MCA.Namespace = mrt.Namespace
	}

	// Set default Application namespace value
	if mrt.Spec.ArgoCDApplication.Namespace == "" {
		mrt.Spec.MCA.Namespace = ArgoCDDefaultNamespace
	}

	// Set default Location values
	if mrt.Spec.Location.Folder == "" {
		mrt.Spec.Location.Folder = LocationDefaultFolder
	}

	return nil
}

func (w *ManifestRequestTemplateWebhook) isApprovalRuleValid(rule governancev1alpha1.ApprovalRule, membersMap map[string]bool, path *field.Path) (bool, *field.Error) {
	isValid, err := w.approvalRuleValidCheck(rule, membersMap, path)
	if !isValid {
		return isValid, err
	}

	if len(rule.Require) > 0 {
		for _, child := range rule.Require {
			isValid, err := w.approvalRuleValidCheck(child, membersMap, path.Child("require"))
			if !isValid {
				return isValid, err
			}
		}
	}

	return true, nil
}

func (w *ManifestRequestTemplateWebhook) approvalRuleValidCheck(rule governancev1alpha1.ApprovalRule, membersMap map[string]bool, path *field.Path) (bool, *field.Error) {
	// Check both missing / existing atLeast and all at the same time
	if rule.AtLeast == nil && rule.All == nil {
		return false, field.Forbidden(path.Child("atLeast"), "atLeast and all cannot be null in the same time")
	} else if rule.AtLeast != nil && rule.All != nil {
		return false, field.Forbidden(path.Child("atLeast"), "atLeast and all cannot be set in the same time")
	}

	// Check, if signer exists in the governors list
	if _, ok := membersMap[rule.Signer]; rule.Signer != "" && !ok {
		return false, field.Forbidden(path.Child("signer"), "signer doesn't exist in governors list")
	}

	// Count total number of possible singers
	cnt := len(rule.Require)
	if rule.Signer != "" {
		cnt++
	}

	// Check if number of signers can be fulfilled
	if cnt == 0 {
		return false, field.Forbidden(path.Child("require"), "no rule or signer is set")
	} else if rule.AtLeast != nil && cnt < *rule.AtLeast {
		return false, field.Forbidden(path.Child("require"), fmt.Sprintf("atLeast is not reachable: %d < %d", cnt, *rule.AtLeast))
	}

	return true, nil
}

func (w *ManifestRequestTemplateWebhook) ValidateCreate(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	mrt, ok := obj.(*governancev1alpha1.ManifestRequestTemplate)
	if !ok {
		return nil, fmt.Errorf("expected a ManifestRequestTemplate object but got %T", obj)
	}

	mrtlog.Info("validating MRT creation", "name", mrt.Name, "namespace", mrt.Namespace)
	var allErrs field.ErrorList

	// Validate, that argocd Application has the default namespace ('argocd')
	if mrt.Spec.ArgoCDApplication.Namespace != "argocd" {
		allErrs = append(allErrs, field.Invalid(field.NewPath("spec").Child("argoCDApplication").Child("namespace"), mrt.Spec.ArgoCDApplication.Namespace, "dynamic namespaces are not supported yet, must be 'argocd'"))
	}

	// Check nested approval rules
	members := mrt.Spec.Governors.Members
	membersMap := make(map[string]bool)
	for _, m := range members {
		membersMap[m.Alias] = true
	}

	if isValid, errorField := w.isApprovalRuleValid(mrt.Spec.Require, membersMap, field.NewPath("spec").Child("require")); !isValid {
		allErrs = append(allErrs, errorField)
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

	// Validate, that argocd Application has the default namespace ('argocd')
	if newMRT.Spec.ArgoCDApplication.Namespace != "argocd" {
		allErrs = append(allErrs, field.Invalid(field.NewPath("spec").Child("argoCDApplication").Child("namespace"), newMRT.Spec.ArgoCDApplication.Namespace, "dynamic namespaces are not supported yet, must be 'argocd'"))
	}

	// Validate, that on update MSR, MCA values cannot be changed
	if oldMRT.Spec.MSR != newMRT.Spec.MSR {
		allErrs = append(allErrs, field.Forbidden(field.NewPath("spec").Child("msr"), "MSR reference is immutable and cannot be changed after creation"))
	}
	if oldMRT.Spec.MCA != newMRT.Spec.MCA {
		allErrs = append(allErrs, field.Forbidden(field.NewPath("spec").Child("mca"), "MCA reference is immutable and cannot be changed after creation"))
	}

	// Check nested approval rules
	members := newMRT.Spec.Governors.Members
	membersMap := make(map[string]bool)
	for _, m := range members {
		membersMap[m.Alias] = true
	}
	if isValid, errorField := w.isApprovalRuleValid(newMRT.Spec.Require, membersMap, field.NewPath("spec").Child("require")); !isValid {
		allErrs = append(allErrs, errorField)
	}

	if len(allErrs) == 0 {
		return nil, nil
	}

	return nil, apierrors.NewInvalid(newMRT.GroupVersionKind().GroupKind(), newMRT.Name, allErrs)
}

func (w *ManifestRequestTemplateWebhook) ValidateDelete(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	return nil, nil
}
