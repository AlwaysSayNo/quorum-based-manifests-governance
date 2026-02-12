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
	"net/url"
	"strings"

	argoappv1 "github.com/argoproj/argo-cd/v3/pkg/apis/application/v1alpha1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	governancev1alpha1 "github.com/AlwaysSayNo/quorum-based-manifests-governance/kubernetes/api/v1alpha1"
	governancecontroller "github.com/AlwaysSayNo/quorum-based-manifests-governance/kubernetes/internal/controller"
)

// +kubebuilder:webhook:path=/mrt/mutate,mutating=true,failurePolicy=fail,sideEffects=None,groups=governance.nazar.grynko.com,resources=manifestrequesttemplates,verbs=create,versions=v1alpha1,name=mrt-mutating-webhook.governance.nazar.grynko.com,admissionReviewVersions=v1
// +kubebuilder:webhook:path=/mrt/validate,mutating=false,failurePolicy=fail,sideEffects=None,groups=governance.nazar.grynko.com,resources=manifestrequesttemplates,verbs=create;update,versions=v1alpha1,name=mrt-validating-webhook.governance.nazar.grynko.com,admissionReviewVersions=v1

var mrtlog = logf.Log.WithName("mrt-resource")

const (
	MSRDefaultName         = "manifestsigningrequest"
	MCADefaultName         = "manifestchangeapproval"
	ArgoCDDefaultNamespace = "argocd"
	LocationDefaultFolder  = "qubmango"
)

type ManifestRequestTemplateWebhook struct {
	Client client.Client
}

func (w *ManifestRequestTemplateWebhook) Default(
	ctx context.Context,
	obj runtime.Object,
) error {
	mrt, ok := obj.(*governancev1alpha1.ManifestRequestTemplate)
	if !ok {
		return fmt.Errorf("expected a ManifestRequestTemplate object but got %T", obj)
	}

	mrtlog.WithValues("mrtName", mrt.Name, "mrtNamespace", mrt.Namespace)
	mrtlog.Info("defaulting MRT")

	// Set creation commit SHA for new MRT
	if !controllerutil.ContainsFinalizer(mrt, governancecontroller.GovernanceFinalizer) {
		if err := w.setCreationCommitAnnotationOnMRT(ctx, mrt); err != nil {
			return fmt.Errorf("set creation commit annotation on MRT: %w", err)
		}
	}

	return nil
}

func (w *ManifestRequestTemplateWebhook) setCreationCommitAnnotationOnMRT(
	ctx context.Context,
	mrt *governancev1alpha1.ManifestRequestTemplate,
) error {
	appName := mrt.Spec.ArgoCD.Application.Name
	appNamespace := mrt.Spec.ArgoCD.Application.Namespace
	appKey := types.NamespacedName{Name: appName, Namespace: appNamespace}

	application := &argoappv1.Application{}
	if err := w.Client.Get(ctx, appKey, application); err != nil {
		mrtlog.Error(err, "Failed to fetch ArgoCD Application for MRT", "appName", appName, "appNamespace", appNamespace)
		return fmt.Errorf("get Application %s:%s for MRT %s:%s: %w", appNamespace, appName, mrt.Namespace, mrt.Name, err)
	}
	if mrt.ObjectMeta.Annotations == nil {
		mrt.ObjectMeta.Annotations = make(map[string]string)
	}
	mrt.ObjectMeta.Annotations[governancecontroller.QubmangoMRTCreationCommitAnnotation] = GetRevisionFromApplication(application)

	return nil
}

func (w *ManifestRequestTemplateWebhook) ValidateCreate(
	ctx context.Context,
	obj runtime.Object,
) (admission.Warnings, error) {
	mrt, ok := obj.(*governancev1alpha1.ManifestRequestTemplate)
	if !ok {
		return nil, fmt.Errorf("expected a ManifestRequestTemplate object but got %T", obj)
	}

	mrtlog.Info("validating MRT creation", "name", mrt.Name, "namespace", mrt.Namespace)
	var allErrs field.ErrorList

	// Validate, that argocd Application has the default namespace ('argocd')
	if mrt.Spec.ArgoCD.Application.Namespace != "argocd" {
		allErrs = append(allErrs, field.Invalid(field.NewPath("spec").Child("argoCDApplication").Child("namespace"), mrt.Spec.ArgoCD.Application.Namespace, "dynamic namespaces are not supported yet, must be 'argocd'"))
	}

	// Check nested approval rules
	if isValid, errorField := w.isApprovalRuleValid(mrt.Spec.Require, w.getMembersMap(mrt), field.NewPath("spec").Child("require")); !isValid {
		allErrs = append(allErrs, errorField)
	}

	// Check MRT and Application have the same repo URLs
	appName := mrt.Spec.ArgoCD.Application.Name
	appNamespace := mrt.Spec.ArgoCD.Application.Namespace
	appKey := types.NamespacedName{Name: appName, Namespace: appNamespace}
	application := &argoappv1.Application{}
	if err := w.Client.Get(ctx, appKey, application); err != nil {
		return nil, fmt.Errorf("get Application %s:%s for MRT %s:%s: %w", appNamespace, appName, mrt.Namespace, mrt.Name, err)
	}

	same, err := SameGitRepository(mrt.Spec.GitRepository.SSH.URL, application.Spec.Source.RepoURL)
	if err != nil {
		return nil, fmt.Errorf("compare Application and MRT git repository URLs: %w", err)
	}
	if !same {
		allErrs = append(allErrs, field.Invalid(field.NewPath("spec").Child("gitRepository").Child("ssh").Child("url"), mrt.Spec.GitRepository.SSH.URL, "SSH repository URL must have the same host, organization and name as Application RepoURL"))
	}

	// Has any error
	if len(allErrs) == 0 {
		return nil, nil
	}

	return nil, apierrors.NewInvalid(mrt.GroupVersionKind().GroupKind(), mrt.Name, allErrs)
}

func (w *ManifestRequestTemplateWebhook) ValidateUpdate(
	ctx context.Context,
	oldObj, newObj runtime.Object,
) (admission.Warnings, error) {
	oldMRT, _ := oldObj.(*governancev1alpha1.ManifestRequestTemplate)
	newMRT, _ := newObj.(*governancev1alpha1.ManifestRequestTemplate)

	mrtlog.Info("validating MRT update", "name", newMRT.Name, "namespace", newMRT.Namespace)
	var allErrs field.ErrorList

	// Check version is updated
	governancecontroller.ValidateVersionUpdated(oldMRT, newMRT, allErrs)

	// Check immutable fields
	governancecontroller.ValidateImmutableFields(oldMRT, newMRT, allErrs)

	// Check nested approval rules
	if isValid, errorField := w.isApprovalRuleValid(newMRT.Spec.Require, w.getMembersMap(newMRT), field.NewPath("spec").Child("require")); !isValid {
		allErrs = append(allErrs, errorField)
	}

	if len(allErrs) == 0 {
		return nil, nil
	}

	return nil, apierrors.NewInvalid(newMRT.GroupVersionKind().GroupKind(), newMRT.Name, allErrs)
}

func (w *ManifestRequestTemplateWebhook) ValidateDelete(
	ctx context.Context,
	obj runtime.Object,
) (admission.Warnings, error) {
	return nil, nil
}

func (w *ManifestRequestTemplateWebhook) getMembersMap(
	mrt *governancev1alpha1.ManifestRequestTemplate,
) map[string]bool {
	members := mrt.Spec.Governors.Members
	membersMap := make(map[string]bool)
	for _, m := range members {
		membersMap[m.Alias] = true
	}
	return membersMap
}

func (w *ManifestRequestTemplateWebhook) isApprovalRuleValid(
	rule governancev1alpha1.ApprovalRule,
	membersMap map[string]bool,
	path *field.Path,
) (bool, *field.Error) {
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

func (w *ManifestRequestTemplateWebhook) approvalRuleValidCheck(
	rule governancev1alpha1.ApprovalRule,
	membersMap map[string]bool,
	path *field.Path,
) (bool, *field.Error) {
	// Check both missing / existing atLeast and all at the same time / non-leaf node has a signer in it.
	if rule.AtLeast == nil && rule.All == nil && len(rule.Require) != 0 {
		return false, field.Forbidden(path.Child("atLeast"), "atLeast and all cannot be null in the same time")
	} else if rule.AtLeast != nil && rule.All != nil {
		return false, field.Forbidden(path.Child("atLeast"), "atLeast and all cannot be set in the same time")
	} else if (rule.AtLeast != nil || rule.All != nil || len(rule.Require) != 0) && rule.Signer != "" {
		return false, field.Forbidden(path.Child("signer"), "signer should be placed only on a leaf node with no atLeast, all, require attributes")
	}

	// Check the leaf-node case
	if rule.Signer != "" {
		return w.approvalRuleValidCheckLeaf(rule, membersMap, path)
	}

	// Check the inner-node cases
	return w.approvalRuleValidCheckNode(rule, path)
}

func (w *ManifestRequestTemplateWebhook) approvalRuleValidCheckLeaf(
	rule governancev1alpha1.ApprovalRule,
	membersMap map[string]bool,
	path *field.Path,
) (bool, *field.Error) {
	// Check, if signer exists in the governors list
	if _, ok := membersMap[rule.Signer]; !ok {
		return false, field.Forbidden(path.Child("signer"), "signer doesn't exist in governors list")
	}
	// End of lead-node case
	return true, nil
}

func (w *ManifestRequestTemplateWebhook) approvalRuleValidCheckNode(
	rule governancev1alpha1.ApprovalRule,
	path *field.Path,
) (bool, *field.Error) {
	// Count total number of possible singers
	cnt := len(rule.Require)

	// Check if number of signers can be fulfilled
	if cnt == 0 {
		return false, field.Forbidden(path.Child("require"), "no rule or signer is set")
	} else if rule.AtLeast != nil && cnt < *rule.AtLeast {
		return false, field.Forbidden(path.Child("require"), fmt.Sprintf("atLeast is not reachable: %d < %d", cnt, *rule.AtLeast))
	}

	return true, nil
}

// SameGitRepository returns true if two URLs point to the same host/org/repo.
// It ignores protocol (ssh/https) and minor format differences like ".git" suffix.
func SameGitRepository(firstURL, secondURL string) (bool, error) {
	n1, err := normalizeGitURL(firstURL)
	if err != nil {
		return false, fmt.Errorf("normalize first URL: %w", err)
	}
	n2, err := normalizeGitURL(secondURL)
	if err != nil {
		return false, fmt.Errorf("normalize second URL: %w", err)
	}
	return n1 == n2, nil
}

// normalizeGitURL converts SSH/HTTPS Git URLs into "host/org/repo" lowercase form.
//
//	git@github.com:owner/repo.git -> github.com/owner/repo
//	https://github.com/owner/repo.git -> github.com/owner/repo
func normalizeGitURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)

	if strings.HasPrefix(raw, "git@") {
		// SSH: git@host:org/repo.git
		parts := strings.SplitN(raw, ":", 2)
		if len(parts) != 2 {
			return "", fmt.Errorf("invalid SSH URL: %s", raw)
		}
		host := strings.TrimPrefix(parts[0], "git@")
		path := strings.TrimSuffix(parts[1], ".git")
		return strings.ToLower(fmt.Sprintf("%s/%s", host, path)), nil
	}

	if strings.HasPrefix(raw, "ssh://") {
		// SSH: ssh://[user@]host/org/repo.git
		u, err := url.Parse(raw)
		if err != nil {
			return "", err
		}
		path := strings.TrimPrefix(u.Path, "/")
		path = strings.TrimSuffix(path, ".git")
		return strings.ToLower(fmt.Sprintf("%s/%s", u.Host, path)), nil
	}

	// HTTPS or other URL-parsable format
	u, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	path := strings.TrimPrefix(u.Path, "/")
	path = strings.TrimSuffix(path, ".git")
	return strings.ToLower(fmt.Sprintf("%s/%s", u.Host, path)), nil
}
