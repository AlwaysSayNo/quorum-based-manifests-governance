# quorum-based-manifests-governance

TODO: 
1. Move controller from deployment key to Git applications. Otherwise requires the machine to trust the github host (have known_hosts in their ~/.ssh/known_hosts)
2. Transactions (Done)
3. Remove index from file on MRT deletion (Done)
4. Point ArgoCD on specific commit after MCA is created (Done)
5. Move all Taskfile from inside to outside (or just to the subproject root)
6. Old / multiple revisions
7. ArgoCD Application should have ignoreDifferences for targetRevision field. We wont implement any automation so far. (Done. Important)
8. Maybe attach all events to queue and make queue owner of events (Done)
9. Add MSR name to the index file as well. (Done)
10. How can we trust the key from MSR and MRT as a CLI user?
11. Rewrite the require rules, where only the leaf node can have signer (Done)
12. Add sourcePath to the MSR from Application manifest (in Spec) (Done)
13. Add handling of SSH / PGP passphrases input. (Done)
14. Add some alias for monorepo in index file. Don't require, if there is only one entry in the index file. (Done: we will require the mrt alias, if there is more than 1 project in the index file)
15. Add real keys to MRT. (Done)
16. GovernanceFolder cannot be changed after creation. Or think about scenario, how it could be changed. It would require: what to do with old MSRs, MCAs. Change entry in the index file on update.
17. Reset Application after MRT deletion back to the desired targetRevision (Done)
18. Split MRT (maybe MSR, MCA) deletion on sub-steps.
19. What to do with MSR, MCA after MRT deletion and finish of governance process? Should we remove signatures from the repository, leave for now, because it is additional logic, leave and backup?
20. Justify, why default MSR has no signatures and integrate it with CLI (to avoid showing in CLI PENDING) (Done)
21. Implement rejection of ArgoCD requests, when they are rejected by MRT but hit some limit, to avoid dead loops (Skipped)
22. Fix infinite sync loop (maybe the reason, is that we block sync and allow only the next commit. it partially synced and stopped) (Done)
23. Remove Status from MSR, because it's not needed. Only latest MSR matters. Or even better move it to Status (CLI) (Done)
24. Release all locks, in case of failures. (Done)
25. Move notification from MSR to MCA (Done)
26. Update MCA documentation (create, reconcile) (Done)
27. Add proactive in MRT (Done)
28. Prevent ArgoCD from syncing targetRevision from repository
29. MRT / MSR should point to the exact git of the request, not the next one 
30. Generate concat documents
31. Is it secure to save passphrases on the local computer?
32. Make support of any argocd namespace
33. What should we do with rollback to previously approved changes (MCA v_k -> MCA v_k-1)?