package slack

import (
	"context"
	"fmt"
	"strings"

	"github.com/go-logr/logr"
	"github.com/slack-go/slack"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	governancev1alpha1 "github.com/AlwaysSayNo/quorum-based-manifests-governance/kubernetes/api/v1alpha1"
)

type slackNotifier struct {
	client client.Client
	logger logr.Logger
}

func NewNotifier(k8sClient client.Client) *slackNotifier {
	return &slackNotifier{
		client: k8sClient,
	}
}

func (s *slackNotifier) NotifyGovernorsMSR(
	ctx context.Context,
	msr *governancev1alpha1.ManifestSigningRequest,
) error {
	s.logger = log.FromContext(ctx)

	// Get MRT
	mrt := &governancev1alpha1.ManifestRequestTemplate{}
	mrtRef := msr.Spec.MRT
	if err := s.client.Get(ctx, types.NamespacedName{Name: mrtRef.Name, Namespace: mrtRef.Namespace}, mrt); err != nil {
		return fmt.Errorf("failed to get parent MRT '%s/%s' for notification config: %w", mrtRef.Namespace, mrtRef.Name, err)
	}

	// Check, if there is any channel to notify
	if mrt.Spec.Notifications == nil || mrt.Spec.Notifications.Slack == nil {
		s.logger.V(3).Info("Slack notifications are not configured for this MRT.")
		return nil
	}

	// Get slack token
	token, err := s.getToken(ctx, mrt)
	if err != nil {
		return fmt.Errorf("get slack token: %w", err)
	}

	// Get slack channels only
	slackChannels := s.getSlackChannels(mrt.Spec.Governors.NotificationChannels)

	if len(slackChannels) == 0 {
		s.logger.V(3).Info("Slack channel configuration is missing for error notification")
		return nil
	}

	// Create the message
	api := slack.New(token)

	// Header
	headerText := slack.NewTextBlockObject("mrkdwn", fmt.Sprintf(":bell: New Manifest Signing Request: *%s*", msr.Name), false, false)
	headerSection := slack.NewSectionBlock(headerText, nil, nil)

	// Body
	bodyText1 := slack.NewTextBlockObject("mrkdwn", fmt.Sprintf("*Application:* %s", mrt.Spec.ArgoCD.Application.Name), false, false)
	bodySection1 := slack.NewSectionBlock(bodyText1, nil, nil)
	bodyText2 := slack.NewTextBlockObject("mrkdwn", fmt.Sprintf("*Git URL:* %s", mrt.Spec.GitRepository.SSH.URL), false, false)
	bodySection2 := slack.NewSectionBlock(bodyText2, nil, nil)
	bodyText3 := slack.NewTextBlockObject("mrkdwn", fmt.Sprintf("*Commit:* `%s`", shortCommitSHA(msr.Spec.CommitSHA)), false, false)
	bodySection3 := slack.NewSectionBlock(bodyText3, nil, nil)
	bodyText4 := slack.NewTextBlockObject("mrkdwn", fmt.Sprintf("*Changed Files:* %d", len(msr.Spec.Changes)), false, false)
	bodySection4 := slack.NewSectionBlock(bodyText4, nil, nil)

	// Footer
	contextText := slack.NewTextBlockObject("mrkdwn", "Please review and sign using the `qubmango` CLI.", false, false)
	contextSection := slack.NewContextBlock("", contextText)

	attachment := slack.Attachment{
		Color: "#e5e100",
		Blocks: slack.Blocks{BlockSet: []slack.Block{
			// Header
			headerSection,
			// Body
			bodySection1,
			bodySection2,
			bodySection3,
			bodySection4,
			// Footer
			contextSection,
		}},
	}

	// Send the message to all channels.
	s.sendAttachment(ctx, api, slackChannels, attachment)

	return nil
}

func (s *slackNotifier) NotifyGovernorsMCA(
	ctx context.Context,
	mca *governancev1alpha1.ManifestChangeApproval,
) error {
	s.logger = log.FromContext(ctx)

	// Get MRT
	mrt := &governancev1alpha1.ManifestRequestTemplate{}
	mrtRef := mca.Spec.MRT
	if err := s.client.Get(ctx, types.NamespacedName{Name: mrtRef.Name, Namespace: mrtRef.Namespace}, mrt); err != nil {
		return fmt.Errorf("failed to get parent MRT '%s/%s' for notification config: %w", mrtRef.Namespace, mrtRef.Name, err)
	}

	// Check, if there is any channel to notify
	if mrt.Spec.Notifications == nil || mrt.Spec.Notifications.Slack == nil {
		log.FromContext(ctx).Info("Slack notifications are not configured for this MRT. Skipping MCA notification.")
		return nil
	}

	// Get slack token
	token, err := s.getToken(ctx, mrt)
	if err != nil {
		return fmt.Errorf("get slack token: %w", err)
	}

	// Get slack channels only
	slackChannels := s.getSlackChannels(mrt.Spec.Governors.NotificationChannels)

	if len(slackChannels) == 0 {
		s.logger.V(3).Info("Slack channel configuration is missing for error notification")
		return nil
	}

	// Create the message
	api := slack.New(token)

	// Header
	headerText := slack.NewTextBlockObject("mrkdwn", fmt.Sprintf(":white_check_mark: Manifest Change *Approved*: `%s`", mca.Name), false, false)
	headerSection := slack.NewSectionBlock(headerText, nil, nil)

	// Body
	bodyText1 := slack.NewTextBlockObject("mrkdwn", fmt.Sprintf("*Application:* %s", mrt.Spec.ArgoCD.Application.Name), false, false)
	bodySection1 := slack.NewSectionBlock(bodyText1, nil, nil)
	bodyText2 := slack.NewTextBlockObject("mrkdwn", fmt.Sprintf("*Git URL:* %s", mrt.Spec.GitRepository.SSH.URL), false, false)
	bodySection2 := slack.NewSectionBlock(bodyText2, nil, nil)
	bodyText3 := slack.NewTextBlockObject("mrkdwn", fmt.Sprintf("*Commit:* `%s`", shortCommitSHA(mca.Spec.CommitSHA)), false, false)
	bodySection3 := slack.NewSectionBlock(bodyText3, nil, nil)

	// List the approvers
	var approverAliases []string
	for _, approver := range mca.Spec.CollectedSignatures {
		approverAliases = append(approverAliases, approver.Signer)
	}
	approversText := "None"
	if len(approverAliases) > 0 {
		approversText = strings.Join(approverAliases, ", ")
	}
	bodyText4 := slack.NewTextBlockObject("mrkdwn", fmt.Sprintf("*Approved by:* %s", approversText), false, false)
	bodySection4 := slack.NewSectionBlock(bodyText4, nil, nil)

	// Slack attachments allow for a colored border.
	attachment := slack.Attachment{
		Color: "#2E8B57",
		Blocks: slack.Blocks{BlockSet: []slack.Block{
			// Header
			headerSection,
			// Body
			bodySection1,
			bodySection2,
			bodySection3,
			bodySection4,
		}},
	}

	// Send the message to all channels.
	s.sendAttachment(ctx, api, slackChannels, attachment)

	return nil
}

func (s *slackNotifier) NotifyError(
	ctx context.Context,
	channels []governancev1alpha1.NotificationChannel,
	message string,
) error {
	s.logger = log.FromContext(ctx)

	// Get slack channels only
	slackChannels := s.getSlackChannels(channels)

	if len(slackChannels) == 0 {
		s.logger.V(3).Info("Slack channel configuration is missing for error notification")
		return nil
	}

	// Create the message
	api := slack.New("")

	// Header
	headerText := slack.NewTextBlockObject("mrkdwn", ":warning: Error Processing Manifest Request", false, false)
	headerSection := slack.NewSectionBlock(headerText, nil, nil)

	// Body
	bodyText := slack.NewTextBlockObject("mrkdwn", fmt.Sprintf("*Details:* %s", message), false, false)
	bodySection := slack.NewSectionBlock(bodyText, nil, nil)

	// Footer
	contextText := slack.NewTextBlockObject("mrkdwn", "Please review the error and take appropriate action.", false, false)
	contextSection := slack.NewContextBlock("", contextText)

	attachment := slack.Attachment{
		Color: "#FF0000",
		Blocks: slack.Blocks{BlockSet: []slack.Block{
			headerSection,
			bodySection,
			contextSection,
		}},
	}

	// Send the message to all channels.
	s.sendAttachment(ctx, api, slackChannels, attachment)

	return nil
}

func (s *slackNotifier) sendAttachment(
	ctx context.Context,
	api *slack.Client,
	slackChannels []*governancev1alpha1.SlackChannel,
	attachment slack.Attachment,
) {
	for _, channel := range slackChannels {
		if channel.ChannelID != "" {
			continue
		}

		channelID := channel.ChannelID

		// Post the message with the attachment.
		_, _, err := api.PostMessageContext(ctx, channelID, slack.MsgOptionAttachments(attachment))
		if err != nil {
			log.FromContext(ctx).Error(err, "Failed to send Slack notification for MCA", "channelID", channelID)
		} else {
			log.FromContext(ctx).Info("Successfully sent Slack notification for MCA", "channelID", channelID)
		}
	}

}

func (s *slackNotifier) getToken(
	ctx context.Context,
	mrt *governancev1alpha1.ManifestRequestTemplate,
) (string, error) {
	// Fetch the Slack Bot Token from the Secret referenced in the MRT spec.
	slackSecretRef := mrt.Spec.Notifications.Slack.SecretsRef
	slackSecret := &corev1.Secret{}
	err := s.client.Get(ctx, types.NamespacedName{Name: slackSecretRef.Name, Namespace: slackSecretRef.Namespace}, slackSecret)
	if err != nil {
		return "", fmt.Errorf("failed to get slack token secret '%s/%s': %w", slackSecretRef.Namespace, slackSecretRef.Name, err)
	}

	tokenBytes, ok := slackSecret.Data["token"]
	if !ok {
		return "", fmt.Errorf("slack token secret '%s/%s' is missing the 'token' key", slackSecretRef.Namespace, slackSecretRef.Name)
	}
	token := string(tokenBytes)
	if token == "" {
		return "", fmt.Errorf("token in slack secret '%s/%s' is empty", slackSecretRef.Namespace, slackSecretRef.Name)
	}

	return token, nil
}

func (s *slackNotifier) getSlackChannels(
	channels []governancev1alpha1.NotificationChannel,
) []*governancev1alpha1.SlackChannel {
	var res []*governancev1alpha1.SlackChannel

	for _, channel := range channels {
		if channel.Slack != nil && channel.Slack.ChannelID != "" {
			continue
		}

		res = append(res, channel.Slack)
	}

	return res
}

func shortCommitSHA(
	commitSHA string,
) string {
	if len(commitSHA) > 7 {
		return commitSHA[:7]
	}
	return commitSHA
}
