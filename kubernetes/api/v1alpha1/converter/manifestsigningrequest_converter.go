package converter

import (
	dto "github.com/AlwaysSayNo/quorum-based-manifests-governance/pkg/api/dto"

	governancev1alpha1 "github.com/AlwaysSayNo/quorum-based-manifests-governance/kubernetes/api/v1alpha1"
)

// K8sToDTO converts Kubernetes MSR to DTO MSR
func K8sToDTO(k8sMSR *governancev1alpha1.ManifestSigningRequest) *dto.ManifestSigningRequestManifestObject {
	if k8sMSR == nil {
		return nil
	}

	return &dto.ManifestSigningRequestManifestObject{
		TypeMeta: k8sMSR.TypeMeta,
		ObjectMeta: dto.ManifestRef{
			Name:      k8sMSR.Name,
			Namespace: k8sMSR.Namespace,
		},
		Spec: K8sSpecToDTO(k8sMSR.Spec),
	}
}

// k8sSpecToDTO converts k8s Spec to DTO Spec
func K8sSpecToDTO(spec governancev1alpha1.ManifestSigningRequestSpec) dto.ManifestSigningRequestSpec {
	return dto.ManifestSigningRequestSpec{
		Version:           spec.Version,
		CommitSHA:         spec.CommitSHA,
		PreviousCommitSHA: spec.PreviousCommitSHA,
		MRT: dto.VersionedManifestRef{
			Name:      spec.MRT.Name,
			Namespace: spec.MRT.Namespace,
			Version:   spec.MRT.Version,
		},
		PublicKey:     spec.PublicKey,
		GitRepository: K8sGitRepoToDTO(spec.GitRepository),
		Locations:     K8sLocationsToDTO(spec.Locations),
		Changes:       K8sChangesToDTO(spec.Changes),
		Governors:     K8sGovernorsToDTO(spec.Governors),
		Require:       K8sRuleToDTO(spec.Require),
	}
}

// K8sGitRepoToDTO converts k8s GitRepository to DTO
func K8sGitRepoToDTO(repo governancev1alpha1.GitRepository) dto.GitRepository {
	return dto.GitRepository{
		SSHURL: repo.SSHURL,
	}
}

// K8sLocationsToDTO converts k8s Locations to DTO
func K8sLocationsToDTO(loc governancev1alpha1.Locations) dto.Locations {
	return dto.Locations{
		GovernancePath: loc.GovernancePath,
		SourcePath:     loc.SourcePath,
	}
}

// K8sChangesToDTO converts k8s FileChange slice to DTO
func K8sChangesToDTO(changes []governancev1alpha1.FileChange) []dto.FileChange {
	if len(changes) == 0 {
		return []dto.FileChange{}
	}

	out := make([]dto.FileChange, len(changes))
	for i, c := range changes {
		out[i] = dto.FileChange{
			Path:      c.Path,
			Status:    dto.FileChangeStatus(c.Status),
			Kind:      c.Kind,
			Name:      c.Name,
			Namespace: c.Namespace,
			SHA256:    c.SHA256,
		}
	}
	return out
}

// K8sGovernorsToDTO converts k8s GovernorList to DTO
func K8sGovernorsToDTO(govs governancev1alpha1.GovernorList) dto.GovernorList {
	return dto.GovernorList{
		NotificationChannels: K8sChannelsToDTO(govs.NotificationChannels),
		Members:              K8sGovernorsSliceToDTO(govs.Members),
	}
}

// K8sChannelsToDTO converts k8s NotificationChannel slice to DTO
func K8sChannelsToDTO(channels []governancev1alpha1.NotificationChannel) []dto.NotificationChannel {
	if len(channels) == 0 {
		return []dto.NotificationChannel{}
	}

	out := make([]dto.NotificationChannel, len(channels))
	for i, c := range channels {
		out[i] = dto.NotificationChannel{
			Slack: K8sSlackToDTO(c.Slack),
		}
	}
	return out
}

// K8sSlackToDTO converts k8s SlackChannel to DTO
func K8sSlackToDTO(slack *governancev1alpha1.SlackChannel) *dto.SlackChannel {
	if slack == nil {
		return nil
	}
	return &dto.SlackChannel{
		ChannelID: slack.ChannelID,
	}
}

// K8sGovernorsSliceToDTO converts k8s Governor slice to DTO
func K8sGovernorsSliceToDTO(govs []governancev1alpha1.Governor) []dto.Governor {
	if len(govs) == 0 {
		return []dto.Governor{}
	}

	out := make([]dto.Governor, len(govs))
	for i, g := range govs {
		out[i] = dto.Governor{
			Alias:     g.Alias,
			PublicKey: g.PublicKey,
		}
	}
	return out
}

// K8sRuleToDTO converts k8s ApprovalRule to DTO
func K8sRuleToDTO(rule governancev1alpha1.ApprovalRule) dto.ApprovalRule {
	return dto.ApprovalRule{
		AtLeast: rule.AtLeast,
		All:     rule.All,
		Require: K8sRulesToDTO(rule.Require),
		Signer:  rule.Signer,
	}
}

// K8sRulesToDTO converts k8s ApprovalRule slice to DTO
func K8sRulesToDTO(rules []governancev1alpha1.ApprovalRule) []dto.ApprovalRule {
	if len(rules) == 0 {
		return []dto.ApprovalRule{}
	}

	out := make([]dto.ApprovalRule, len(rules))
	for i, r := range rules {
		out[i] = K8sRuleToDTO(r)
	}
	return out
}
