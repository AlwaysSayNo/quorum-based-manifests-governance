package display

import (
	"bytes"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/ProtonMail/go-crypto/openpgp/armor"
	"github.com/fatih/color"
	"github.com/olekukonko/tablewriter"
	"github.com/xlab/treeprint"

	manager "github.com/AlwaysSayNo/quorum-based-manifests-governance/cli/internal/repository"
	msrvalidation "github.com/AlwaysSayNo/quorum-based-manifests-governance/cli/internal/validation"
)

type SignatureStatus string

const (
	Verified           SignatureStatus = "✅ Signed"
	Pending            SignatureStatus = "⏳ Pending"
	MalformedPublicKey SignatureStatus = "⚠️ Malformed public key"
)

func PrintIfVerifyFails(
	w io.Writer,
	msr *manager.ManifestSigningRequestManifestObject,
	msrBytes []byte,
	appSignature manager.SignatureData,
	governorSignatures []manager.SignatureData,
	changedFiles map[string]manager.FileBytesWithStatus,
) error {
	// Verify, that MSR was signed by the MRT publicKey (from MSR Spec).
	msg, err := msrvalidation.VerifyMSRSignature(msr, msrBytes, appSignature)
	// Verify, that files' content isn't changed.
	if err == nil {
		msg, err = msrvalidation.VerifyChangedFiles(msr, changedFiles)
	}
	if err != nil {
		printMSRFailed(w, msr, msg, err)
		return err
	}

	return nil
}

// PrintMSRRaw prints the raw MSR manifest to the writer.
func PrintMSRRaw(
	w io.Writer,
	msr *manager.ManifestSigningRequestManifestObject,
	msrBytes []byte,
	appSignature manager.SignatureData,
	governorSignatures []manager.SignatureData,
	changedFiles map[string]manager.FileBytesWithStatus,
) error {
	err := PrintIfVerifyFails(w, msr, msrBytes, appSignature, governorSignatures, changedFiles)
	if err != nil {
		return err
	}
	
	_, err = w.Write(msrBytes)
	return err
}

// PrintMSRTable prints a human-readable summary of the MSR and its signature status.
// It performs cryptographic verification of all signatures (qubmango, governors).
// It verifies, if changed files are not tampered with.
func PrintMSRTable(
	w io.Writer,
	msr *manager.ManifestSigningRequestManifestObject,
	msrBytes []byte,
	appSignature manager.SignatureData,
	governorSignatures []manager.SignatureData,
	changedFiles map[string]manager.FileBytesWithStatus,
) error {
	err := PrintIfVerifyFails(w, msr, msrBytes, appSignature, governorSignatures, changedFiles)
	if err != nil {
		return err
	}

	// Print MSR information in table, if no error happened.
	printMSRInformation(w, msr)

	// Render the signature status tree
	fmt.Fprintln(w, "\nApproval Status:")

	verifiedSigners, signerWarnings := getVerifiedSigners(msr, governorSignatures, msrBytes)

	// Start the recursive evaluation with the root rule from the MSR Spec.
	approvalTree := treeprint.New()
	isOverallApproved := evaluateAndBuildRuleTree(msr.Spec.Require, verifiedSigners, approvalTree)

	// Print the tree and summary status.
	fmt.Fprintln(w, approvalTree.String())

	if isOverallApproved {
		fmt.Fprintln(w, "\nOverall Status:", color.GreenString("APPROVED"))
	} else {
		fmt.Fprintln(w, "\nOverall Status:", color.YellowString("PENDING APPROVAL"))
	}

	// Print warnings if there're any.
	if len(signerWarnings) != 0 {
		var signerWarningsArr []string
		for key, val := range signerWarnings {
			signerWarningsArr = append(signerWarningsArr, key+": "+val)
		}

		fmt.Fprintln(w, "\nWarnings:", "\n", color.YellowString(strings.Join(signerWarningsArr, "\n")))
	}

	fmt.Fprintln(w, "")
	return nil
}

// If the MSR itself is not valid, this is a major security warning.
func printMSRFailed(
	w io.Writer,
	msr *manager.ManifestSigningRequestManifestObject,
	msg string,
	err error,
) {
	fmt.Fprintf(w, "Active Manifest Signing Request: %s\n\n", msr.ObjectMeta.Namespace+":"+msr.ObjectMeta.Name)
	fmt.Fprintf(w, "QuBManGo Operator: %s\n", color.RedString("Failed"))

	errStr := fmt.Sprintf("Error: %v\n", err)
	fmt.Fprintf(w, "%s", color.RedString(errStr))
	fmt.Fprintln(w, "")

	msg = fmt.Sprintf("%s", msg)
	fmt.Fprintln(w, color.RedString(msg))
}

func printMSRInformation(
	w io.Writer,
	msr *manager.ManifestSigningRequestManifestObject,
) {
	// Render MSR general info
	fmt.Fprintf(w, "Active Manifest Signing Request: %s\n\n", msr.ObjectMeta.Namespace+":"+msr.ObjectMeta.Name)
	fmt.Fprintf(w, "QuBManGo Operator: %s\n", color.GreenString("Verified"))
	fmt.Fprintf(w, "Changed Files: %s\n", color.GreenString("Verified"))
	fmt.Fprintln(w, "")

	msrTable := tablewriter.NewTable(w)
	msrTable.Header("Version", "Repository", "MRT", "Status", "CommitSHA")
	msrTable.Append([]string{
		strconv.Itoa(msr.Spec.Version),
		msr.Spec.GitRepository.SSHURL,
		msr.Spec.MRT.Namespace + ":" + msr.Spec.MRT.Name,
		string(msr.Spec.Status),
		msr.Spec.CommitSHA,
	})
	msrTable.Render()

	// Render changed files statuses
	changesTable := tablewriter.NewTable(w)
	changesTable.Header("Kind", "Namespace", "Name", "Status")
	for _, cf := range msr.Spec.Changes {
		changesTable.Append([]string{
			cf.Kind,
			cf.Namespace,
			cf.Name,
			string(cf.Status),
		})
	}
	changesTable.Render()
}

// evaluateAndBuildRuleTree is a recursive function that evaluates the approval rules
// and builds a visual tree representation.
// It returns 'true' if the current rule node is satisfied.
func evaluateAndBuildRuleTree(
	rule manager.ApprovalRule,
	verifiedSigners map[string]SignatureStatus,
	treeNode treeprint.Tree,
) bool {
	// Leaf case: The node is a specific signer
	if rule.Signer != "" {
		status := verifiedSigners[rule.Signer]
		var statusText string
		isSigned := false

		switch status {
		case Verified:
			statusText = color.GreenString("SIGNED ✅")
			isSigned = true
		case MalformedPublicKey:
			statusText = color.RedString("KEY ERROR ⚠️")
		default:
			statusText = color.YellowString("PENDING ⏳")
		}

		treeNode.AddNode(fmt.Sprintf("%s: %s", rule.Signer, statusText))
		return isSigned
	}

	// Node case: The node is a logical group (require-all or require-at-least)
	var requiredCount int
	var nodeLabel string
	var isAllRequired bool

	if rule.All != nil && *rule.All {
		// require-all rule
		requiredCount = len(rule.Require)
		isAllRequired = true
		nodeLabel = fmt.Sprintf("Require ALL (%d of %d)", 0, requiredCount) // Placeholder count
	} else if rule.AtLeast != nil {
		// require-at-least rule
		requiredCount = *rule.AtLeast
		nodeLabel = fmt.Sprintf("Require AT LEAST %d (%d of %d)", len(rule.Require), 0, len(rule.Require)) // Placeholder count
	} else {
		treeNode.AddNode(color.RedString("Invalid Rule Node"))
		return false
	}

	// Create a new branch for this logical group
	branch := treeNode.AddBranch(nodeLabel)

	// Recursively evaluate children and count how many are satisfied
	satisfiedCount := 0
	for _, childRule := range rule.Require {
		if evaluateAndBuildRuleTree(childRule, verifiedSigners, branch) {
			satisfiedCount++
		}
	}

	// Update the branch label
	isRuleFulfilled := satisfiedCount >= requiredCount
	finalStatusIcon := color.YellowString("⏳")
	if isRuleFulfilled {
		finalStatusIcon = color.GreenString("✅")
	} else if isAllRequired && satisfiedCount < requiredCount {
		// If it's a "require-all" and not all are met, it has definitively failed.
		// For "at-least", it's just pending until the threshold is met.
	}

	if rule.All != nil && *rule.All {
		branch.SetValue(fmt.Sprintf("Require ALL (%d of %d) %s", satisfiedCount, requiredCount, finalStatusIcon))
	} else if rule.AtLeast != nil {
		branch.SetValue(fmt.Sprintf("Require AT LEAST %d (%d of %d) %s", *rule.AtLeast, satisfiedCount, *rule.AtLeast, finalStatusIcon))
	}

	return isRuleFulfilled
}

// getVerifiedSigners maps governors to signatures and returns warnings, if any noticed.
func getVerifiedSigners(
	msr *manager.ManifestSigningRequestManifestObject,
	governorSignatures []manager.SignatureData,
	msrBytes []byte,
) (map[string]SignatureStatus, map[string]string) {
	// Build a map<alias, pubKey> of governor public keys for lookup.
	governorKeys := make(map[string]string)
	for _, gov := range msr.Spec.Governors.Members {
		governorKeys[gov.Alias] = gov.PublicKey
	}

	// Build a map<alias, status> of governors signatures.
	verifiedSigners := make(map[string]SignatureStatus)
	for _, gov := range msr.Spec.Governors.Members {
		verifiedSigners[gov.Alias] = Pending
	}
	// Any warnings found.
	signerWarnings := make(map[string]string)

	for _, sigData := range governorSignatures {
		// For each signature, try to verify it against every known governor's public key.
		for alias, pubKeyStr := range governorKeys {
			// Skip already processed keys.
			if verifiedSigners[alias] != Pending {
				continue
			}

			keyRing, err := openpgp.ReadArmoredKeyRing(bytes.NewReader([]byte(pubKeyStr)))
			if err != nil {
				// This governor has a malformed public key in the MSR Spec.
				verifiedSigners[alias] = MalformedPublicKey
				signerWarnings[alias] = err.Error()
				continue
			}

			// De-armor the signature before verification
			armorBlock, err := armor.Decode(bytes.NewReader(sigData))
			if err != nil {
				// Signature might be malformed, try next signer
				continue
			}

			_, err = openpgp.CheckDetachedSignature(keyRing, bytes.NewReader(msrBytes), armorBlock.Body, nil)
			if err == nil {
				verifiedSigners[alias] = Verified
				// Move to the next signature
				break
			}
		}
	}

	return verifiedSigners, signerWarnings
}
