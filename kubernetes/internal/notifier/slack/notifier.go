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

	// Create the message
	api := slack.New(token)

	// Header
	headerText := slack.NewTextBlockObject("mrkdwn", fmt.Sprintf(":bell: New Manifest Signing Request: *%s*", msr.Name), false, false)
	headerSection := slack.NewSectionBlock(headerText, nil, nil)

	// Body
	fields := []*slack.TextBlockObject{
		slack.NewTextBlockObject("mrkdwn", fmt.Sprintf("*Application:* %s", mrt.Spec.ArgoCDApplication.Name), false, false),
		slack.NewTextBlockObject("mrkdwn", fmt.Sprintf("*Git URL:* %s", mrt.Spec.GitRepository.SSHURL), false, false),
		slack.NewTextBlockObject("mrkdwn", fmt.Sprintf("*Commit:* `%s`", shortCommitSHA(msr.Spec.CommitSHA)), false, false),
		slack.NewTextBlockObject("mrkdwn", fmt.Sprintf("*Changed Files:* %d", len(msr.Spec.Changes)), false, false),
	}
	fieldsSection := slack.NewSectionBlock(nil, fields, nil)

	// Footer
	contextText := slack.NewTextBlockObject("mrkdwn", "Please review and sign using the `qubmango` CLI.", false, false)
	contextSection := slack.NewContextBlock("", contextText)

	msg := slack.NewBlockMessage(
		headerSection,
		fieldsSection,
		contextSection,
	)

	// Send the message to all channels.
	for _, channel := range msr.Spec.Governors.NotificationChannels {
		if channel.Slack != nil && channel.Slack.ChannelID != "" {
			channelID := channel.Slack.ChannelID
			_, _, err := api.PostMessageContext(ctx, channelID, slack.MsgOptionBlocks(msg.Blocks.BlockSet...))
			if err != nil {
				s.logger.V(2).Error(err, "Failed to send Slack notification", "channelID", channelID)
			} else {
				s.logger.V(2).Info("Successfully sent Slack notification", "channelID", channelID)
			}
		}
	}

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

	// Create the message
	api := slack.New(token)

	// Header
	headerText := slack.NewTextBlockObject("mrkdwn", fmt.Sprintf(":white_check_mark: Manifest Change *Approved*: `%s`", mca.Name), false, false)

	headerSection := slack.NewSectionBlock(headerText, nil, nil)

	// Body
	fields := []*slack.TextBlockObject{
		slack.NewTextBlockObject("mrkdwn", fmt.Sprintf("*Application:* %s", mrt.Spec.ArgoCDApplication.Name), false, false),
		slack.NewTextBlockObject("mrkdwn", fmt.Sprintf("*Git URL:* %s", mrt.Spec.GitRepository.SSHURL), false, false),
		slack.NewTextBlockObject("mrkdwn", fmt.Sprintf("*Commit:* `%s`", shortCommitSHA(mca.Spec.CommitSHA)), false, false),
	}

	// List the approvers
	var approverAliases []string
	for _, approver := range mca.Spec.CollectedSignatures {
		approverAliases = append(approverAliases, approver.Signer)
	}
	approversText := "None"
	if len(approverAliases) > 0 {
		approversText = strings.Join(approverAliases, ", ")
	}
	fields = append(fields, slack.NewTextBlockObject("mrkdwn", fmt.Sprintf("*Approved by:* %s", approversText), false, false))

	fieldsSection := slack.NewSectionBlock(nil, fields, nil)

	// Slack attachments allow for a colored border.
	attachment := slack.Attachment{
		Color: "#2E8B57",
		Blocks: slack.Blocks{BlockSet: []slack.Block{
			headerSection,
			fieldsSection,
		}},
	}

	// Send the message to all channels.
	for _, channel := range mrt.Spec.Governors.NotificationChannels {
		if channel.Slack != nil && channel.Slack.ChannelID != "" {
			channelID := channel.Slack.ChannelID

			// Post the message with the attachment.
			_, _, err := api.PostMessageContext(ctx, channelID, slack.MsgOptionAttachments(attachment))
			if err != nil {
				log.FromContext(ctx).Error(err, "Failed to send Slack notification for MCA", "channelID", channelID)
			} else {
				log.FromContext(ctx).Info("Successfully sent Slack notification for MCA", "channelID", channelID)
			}
		}
	}

	return nil
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

func shortCommitSHA(
	commitSHA string,
) string {
	if len(commitSHA) > 7 {
		return commitSHA[:7]
	}
	return commitSHA
}
