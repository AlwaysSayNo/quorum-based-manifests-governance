package display

import (
	"bytes"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/fatih/color"
	"github.com/go-git/go-git/v5/plumbing/format/diff"
	"github.com/olekukonko/tablewriter"
	"github.com/xlab/treeprint"

	dto "github.com/AlwaysSayNo/quorum-based-manifests-governance/pkg/api/dto"
	msrvalidation "github.com/AlwaysSayNo/quorum-based-manifests-governance/pkg/validation/msr"
)

func PrintIfVerifyFails(
	w io.Writer,
	msr *dto.ManifestSigningRequestManifestObject,
	msrBytes []byte,
	appSignature dto.SignatureData,
	governorSignatures []dto.SignatureData,
	changedFiles map[string]dto.FileBytesWithStatus,
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

// PrintMSRFileDiffs
// PrintMSRFileDiffs prints a colored git-diff of the files MSR.
func PrintMSRFileDiffs(
	w io.Writer,
	msr *dto.ManifestSigningRequestManifestObject,
	msrBytes []byte,
	appSignature dto.SignatureData,
	governorSignatures []dto.SignatureData,
	changedFiles map[string]dto.FileBytesWithStatus,
	patches map[string]diff.Patch,
) error {
	err := PrintIfVerifyFails(w, msr, msrBytes, appSignature, governorSignatures, changedFiles)
	if err != nil {
		return err
	}

	// Render MSR general info
	fmt.Fprintf(w, "Manifest Signing Request: %s v%d\n\n", msr.ObjectMeta.Namespace+":"+msr.ObjectMeta.Name, msr.Spec.Version)
	fmt.Fprintf(w, "QuBManGo Operator: %s\n", color.GreenString("Verified"))
	fmt.Fprintf(w, "Changed Files: %s\n", color.GreenString("Verified"))
	fmt.Fprintln(w, "")
	fmt.Fprintf(w, "Files Diffs (from commit %s to %s):\n\n", msr.Spec.PreviousCommitSHA[:7], msr.Spec.CommitSHA[:7])

	for _, change := range msr.Spec.Changes {
		// Print a header for each file
		fmt.Fprintln(w, strings.Repeat("=", 80))
		fmt.Fprintln(w, "File:", color.CyanString(change.Path))
		fmt.Fprintln(w, "Status:", statusColored(change.Status))
		fmt.Fprintln(w, strings.Repeat("-", 80))

		// Convert patch into string
		pStr, err := patchToString(patches[change.Path])
		if err != nil {
			fmt.Fprintln(w, color.RedString("Could not generate diff: %v", err))
			continue
		}

		// Print the colored diff
		printColoredDiff(w, pStr)
	}

	return nil
}

// statusColored converts status enum into colored string based on its value.
func statusColored(
	status dto.FileChangeStatus,
) string {
	var statusStr string
	switch status {
	case dto.New:
		statusStr = color.GreenString(string(status))
	case dto.Updated:
		statusStr = color.YellowString(string(status))
	case dto.Deleted:
		statusStr = color.RedString(string(status))
	default:
		statusStr = string(status)
	}

	return statusStr
}

// printColoredDiff is a helper to print git-style diffs with color.
func printColoredDiff(w io.Writer, diff string) {
	lines := strings.Split(diff, "\n")
	for _, line := range lines {
		switch {
		case strings.HasPrefix(line, "---"), strings.HasPrefix(line, "+++"), strings.HasPrefix(line, "@@"):
			color.New(color.FgCyan).Fprintln(w, line)
		case strings.HasPrefix(line, "+"):
			color.New(color.FgGreen).Fprintln(w, line)
		case strings.HasPrefix(line, "-"):
			color.New(color.FgRed).Fprintln(w, line)
		default:
			fmt.Fprintln(w, line)
		}
	}
}

// patchToString converts diff.Patch into string.
func patchToString(
	patch diff.Patch,
) (string, error) {
	var buff bytes.Buffer
	encoder := diff.NewUnifiedEncoder(&buff, diff.DefaultContextLines)
	if err := encoder.Encode(patch); err != nil {
		return "", fmt.Errorf("failed to encode file patch: %w", err)
	}

	return buff.String(), nil
}

// PrintMSRRaw prints the raw MSR manifest to the writer.
func PrintMSRRaw(
	w io.Writer,
	msr *dto.ManifestSigningRequestManifestObject,
	msrBytes []byte,
	appSignature dto.SignatureData,
	governorSignatures []dto.SignatureData,
	changedFiles map[string]dto.FileBytesWithStatus,
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
	msr *dto.ManifestSigningRequestManifestObject,
	msrBytes []byte,
	appSignature dto.SignatureData,
	governorSignatures []dto.SignatureData,
	changedFiles map[string]dto.FileBytesWithStatus,
) error {
	err := PrintIfVerifyFails(w, msr, msrBytes, appSignature, governorSignatures, changedFiles)
	if err != nil {
		return err
	}

	// Print MSR information in table, if no error happened.
	printMSRInformation(w, msr)

	// Render the signature status tree
	fmt.Fprintln(w, "\nApproval Status:")

	verifiedSigners, signerWarnings := msrvalidation.GetVerifiedSigners(msr, governorSignatures, msrBytes)

	// Start the recursive evaluation with the root rule from the MSR Spec.
	approvalTree := treeprint.New()
	isOverallApproved := msrvalidation.EvaluateAndBuildRuleTree(msr.Spec.Require, verifiedSigners, approvalTree)

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

// printMSRFailed prints failed information, rendering the msg and error.
func printMSRFailed(
	w io.Writer,
	msr *dto.ManifestSigningRequestManifestObject,
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
	msr *dto.ManifestSigningRequestManifestObject,
) {
	// Render MSR general info
	fmt.Fprintf(w, "Manifest Signing Request: %s v%d\n\n", msr.ObjectMeta.Namespace+":"+msr.ObjectMeta.Name, msr.Spec.Version)
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
