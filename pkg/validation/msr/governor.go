package validation

import (
	"bytes"
	"fmt"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/ProtonMail/go-crypto/openpgp/armor"
	"github.com/fatih/color"
	"github.com/xlab/treeprint"

	dto "github.com/AlwaysSayNo/quorum-based-manifests-governance/pkg/api/dto"
)

type RuleEvaluation struct {
	Satisfied      bool
	SatisfiedCount int
	RequiredCount  int
}

type SignatureStatus string

const (
	Verified           SignatureStatus = "Signed"
	Pending            SignatureStatus = "Pending"
	MalformedPublicKey SignatureStatus = "MalformedPublicKey"
)

// EvaluateAndBuildRuleTree is a recursive function that evaluates the approval rules
// and builds a visual tree representation.
// It returns 'true' if the current rule node is satisfied.
func EvaluateAndBuildRuleTree(
	rule dto.ApprovalRule,
	verifiedSigners map[string]SignatureStatus,
	treeNode treeprint.Tree,
) bool {
	fulfilled, _, _ := evaluateRuleNode(rule, verifiedSigners, treeNode)
	return fulfilled
}

// EvaluateRules runs a boolean-only evaluation of the given approval rule.
// It returns 'true' if the current rule node is satisfied.
func EvaluateRules(rule dto.ApprovalRule, verifiedSigners map[string]SignatureStatus) bool {
	ok, _, _ := evaluateRuleNode(rule, verifiedSigners, treeprint.New())
	return ok
}

func evaluateRuleNode(
	rule dto.ApprovalRule,
	verifiedSigners map[string]SignatureStatus,
	treeNode treeprint.Tree,
) (bool, int, int) {
	// Leaf case
	if rule.Signer != "" {
		return evaluateLeaf(rule, verifiedSigners, treeNode)
	}

	// Node case
	return evaluateGroup(rule, verifiedSigners, treeNode)
}

// evaluateLeaf evaluates a single signer rule.
// It returns true if the signer has a Verified status.
func evaluateLeaf(
	rule dto.ApprovalRule,
	verifiedSigners map[string]SignatureStatus,
	treeNode treeprint.Tree,
) (bool, int, int) {
	// The node is a specific signer
	status := verifiedSigners[rule.Signer]
	isSigned := status == Verified

	// Add status of the signature
	var statusText string
	switch status {
	case Verified:
		statusText = color.GreenString("SIGNED ✅")
	case MalformedPublicKey:
		statusText = color.RedString("KEY ERROR ⚠️")
	default:
		statusText = color.YellowString("PENDING ⏳")
	}
	treeNode.AddNode(fmt.Sprintf("%s: %s", rule.Signer, statusText))

	return isSigned, btoi(isSigned), 1
}

// evaluateGroup evaluates a logical group rule ("require ALL" or "require AT LEAST").
// It recursively evaluates all child rules, counts how many are satisfied, and
// compares against the required count.
func evaluateGroup(
	rule dto.ApprovalRule,
	verifiedSigners map[string]SignatureStatus,
	treeNode treeprint.Tree,
) (bool, int, int) {
	// Group case
	requiredCount, ok := groupRequiredCount(rule)
	if !ok {
		treeNode.AddNode(color.RedString("Invalid Rule Node"))
		return false, 0, 0
	}

	// Create a branch with placeholder value
	var branch = treeNode.AddBranch("...")
	// Evaluate children nodes and count their rules fulfillment
	satisfiedCount := 0
	for _, childRule := range rule.Require {
		ok, _, _ := evaluateRuleNode(childRule, verifiedSigners, branch)
		if ok {
			satisfiedCount++
		}
	}

	// Update branch value
	statusIcon := color.YellowString("⏳")
	if satisfiedCount >= requiredCount {
		statusIcon = color.GreenString("✅")
	}
	branch.SetValue(fmt.Sprintf("Require %s (%d of %d) %s", ruleLabel(rule), satisfiedCount, requiredCount, statusIcon))

	return satisfiedCount >= requiredCount, satisfiedCount, requiredCount
}

func groupRequiredCount(
	rule dto.ApprovalRule,
) (int, bool) {
	if rule.All != nil && *rule.All {
		// require-all rule
		return len(rule.Require), true
	} else if rule.AtLeast != nil {
		// require-at-least rule
		return *rule.AtLeast, true
	}

	// Bad rule scenario
	return -1, false
}

func ruleLabel(
	rule dto.ApprovalRule,
) string {
	if rule.All != nil && *rule.All {
		// require-all rule
		return "ALL"
	} else if rule.AtLeast != nil {
		// require-at-least rule
		return "AT LEAST"
	}
	return "Invalid Rule Node"
}

func btoi(b bool) int {
	if b {
		return 1
	}
	return 0
}

// GetVerifiedSigners maps governors to signatures and returns warnings, if any noticed.
func GetVerifiedSigners(
	msr *dto.ManifestSigningRequestManifestObject,
	governorSignatures []dto.SignatureData,
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
