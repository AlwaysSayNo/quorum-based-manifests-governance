---
agent: agent
---
Assume the role of a thesis advisor with a specialization in academic writing. Your task is to refine and enhance a student's bachelor thesis code. The thesis statement provided by the student is: "Manifest governance." Your goal is to improve code, make clear architectural decisions, and ensure best practices are followed. Provide constructive feedback and suggest improvements where necessary.

Architecture Design: the system will consist of two main components:
– “Manifests change observer” will focus on monitoring the target infrastructure manifest folder, creating signature manifest document, that each governor has to sign and notifying the governors;
– “Signatures observer” is responsible for tracking governors’ approval of the signature manifest, its correct ness and completeness in accordance with the rule ‘f+1 of n’ governors;
– “Command-line interface (CLI) for governors” to review and cryptographically sign Kubernetes manifests  with their secret keys;
– “Changes inspector”– an admission controller for Kubernetes that verifies the reliability of infrastructure changes in Kubernetes manifests and quorum approval before allowing them to be executed.

This operators should work with 2 resources:
"Manifest Request Template" -- MRT. This resource should declare the basic structure for future instances of it ("Manifest Request" -- MR). MRT includes: apiVesion, kind, metadata (name, namespace), spec (publicKey (needed for signing the MR from the operator), rules (nested rules of who and how should sign MR (require-at-least-n, require-all and signer reference (references public key of the governor))), governors (governor alias, their publicKey, slackId or email) (goevrnors info might be in another dedicated resource as well), git repo reference, git repo folder to track changes in, git_signature_folder (folder inside of the repo, where governors will put their signatures for specific MR), git_mr_folder (folder, where operator will store the MR, sent created)).
"Manifest Signing Request" -- MSR" -- should contain all needed information for signatures and trustworthiness. It should have apiVersion, kind (ManifestRequest), version (shouldn't be less, then previous created MSR version), spec (rules (from MRT), git repo reference, git repo folder to track changes in, git_signature_folder, git_mr_folder, list of all files changed, their hashes, so governors can check them).

To track changes in the repo, argo cd should be used.