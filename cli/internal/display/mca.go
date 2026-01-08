package display

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/fatih/color"
	"github.com/olekukonko/tablewriter"
	"github.com/xlab/treeprint"

	dto "github.com/AlwaysSayNo/quorum-based-manifests-governance/pkg/api/dto"
	validationcommon "github.com/AlwaysSayNo/quorum-based-manifests-governance/pkg/validation"

	manager "github.com/AlwaysSayNo/quorum-based-manifests-governance/cli/internal/repository"
)

func PrintIfMCAVerifyFails(
	w io.Writer,
	mcaInfo *manager.MCAInfo,
	trustedGovernanceKey string,
) error {
	var msg string
	var err error

	if mcaInfo.Obj.Spec.PublicKey != trustedGovernanceKey {
		msg = "CRITICAL WARNING: The MCA public key is different from trusted governance key. Do not proceed"
		err = fmt.Errorf("mca public key is different from trusted key")
	}
	if err == nil {
		// Verify, that MCA was signed by the MRT publicKey (from MCA Spec).
		msg, err = validationcommon.VerifySignature(mcaInfo.Obj.Spec.PublicKey, mcaInfo.Content, mcaInfo.Sign)
	}

	// Verify, that files' content isn't changed.
	if err != nil {
		printMCAFailed(w, mcaInfo.Obj, msg, err)
		return err
	}

	return nil
}

// printMCAFailed prints failed information, rendering the msg and error.
func printMCAFailed(
	w io.Writer,
	mca *dto.ManifestChangeApprovalManifestObject,
	msg string,
	err error,
) {
	// MCA signature is invalid/untrusted
	fmt.Fprintf(w, "Manifest Change Approval: %s\n\n", mca.ObjectMeta.Namespace+":"+mca.ObjectMeta.Name)
	fmt.Fprintf(w, "QuBManGo Operator: %s\n", color.RedString("Failed"))

	errStr := fmt.Sprintf("Error: %v\n", err)
	fmt.Fprintf(w, "%s", color.RedString(errStr))
	fmt.Fprintln(w, "")

	msg = fmt.Sprintf("%s", msg)
	fmt.Fprintln(w, color.RedString(msg))
}

func PrintMCAHistoryTable(
	w io.Writer,
	mcaInfos []manager.MCAInfo,
	trustedGovernanceKey string,
) error {
	if len(mcaInfos) == 0 {
		fmt.Fprintln(w, "No MCA history found")
		return nil
	}

	for idx, mcaInfo := range mcaInfos {
		mca := mcaInfo.Obj

		// Verify MCA signature
		err := PrintIfMCAVerifyFails(w, &mcaInfo, trustedGovernanceKey)
		if err != nil {
			continue
		} else {

			// Print MCA information in table
			verifiedSigners := getMCAVerifiedSigners(mca)
			printMCAInformation(w, mca)

			// Render the signature status tree
			fmt.Fprintln(w, "\nApproval Status:")

			approvalTree := treeprint.New()
			isOverallApproved := validationcommon.EvaluateAndBuildRuleTree(mca.Spec.Require, verifiedSigners, approvalTree)
			fmt.Fprintln(w, approvalTree.String())

			if isOverallApproved {
				fmt.Fprintln(w, "\nOverall Status:", color.GreenString("APPROVED"))
			} else {
				fmt.Fprintln(w, "\nOverall Status:", color.YellowString("PENDING APPROVAL"))
			}

			fmt.Fprintln(w, "")
		}

		// Print separator if not the last entry
		if idx < len(mcaInfos)-1 {
			fmt.Fprintln(w, strings.Repeat("=", 80))
			fmt.Fprintln(w, "")
		}
	}

	return nil
}

// getMCAVerifiedSigners builds a map of verified signers from MCA CollectedSignatures.
// Unlike MSR which has governor signatures as separate files, MCA has CollectedSignatures in the spec.
func getMCAVerifiedSigners(
	mca *dto.ManifestChangeApprovalManifestObject,
) map[string]validationcommon.SignatureStatus {
	// Build a map<alias, status> of governors signatures.
	verifiedSigners := make(map[string]validationcommon.SignatureStatus)
	for _, gov := range mca.Spec.Governors.Members {
		verifiedSigners[gov.Alias] = validationcommon.NotSigned
	}

	// For each collected signature, mark the signer as verified
	for _, collectedSig := range mca.Spec.CollectedSignatures {
		// Find the governor with this signer name/alias
		for _, gov := range mca.Spec.Governors.Members {
			if gov.Alias == collectedSig.Signer {
				verifiedSigners[gov.Alias] = validationcommon.Signed
				break
			}
		}
	}

	return verifiedSigners
}

// printMCAInformation prints the metadata table for a MCA
func printMCAInformation(
	w io.Writer,
	mca *dto.ManifestChangeApprovalManifestObject,
) {
	// Render MCA general info
	fmt.Fprintf(w, "Manifest Change Approval: %s v%d\n\n", mca.ObjectMeta.Namespace+":"+mca.ObjectMeta.Name, mca.Spec.Version)
	fmt.Fprintf(w, "QuBManGo Operator: %s\n", color.GreenString("Verified"))
	fmt.Fprintln(w, "")

	mcaTable := tablewriter.NewTable(w)
	mcaTable.Header("Version", "Repository", "MRT", "CommitSHA")
	mcaTable.Append([]string{
		strconv.Itoa(mca.Spec.Version),
		mca.Spec.GitRepository.SSHURL,
		mca.Spec.MRT.Namespace + ":" + mca.Spec.MRT.Name,
		mca.Spec.CommitSHA,
	})
	mcaTable.Render()

	// Render changed files statuses
	changesTable := tablewriter.NewTable(w)
	changesTable.Header("Kind", "Namespace", "Name", "Status")
	for _, cf := range mca.Spec.Changes {
		changesTable.Append([]string{
			cf.Kind,
			cf.Namespace,
			cf.Name,
			string(cf.Status),
		})
	}
	changesTable.Render()
}
