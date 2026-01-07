# quorum-based-manifests-governance

TODO (Done):
- Transactions in controller methods
- Remove index from file on MRT deletion
- Point ArgoCD to a specific commit after MCA is created (Used targetRevision)
- ArgoCD Application should have `ignoreDifferences` for `targetRevision` field. We wont implement any automation so far
- Maybe attach all events to queue and make queue owner of events
- Add MSR name to the index file as well.
- Rewrite the require rules. So only the leaf node can have `Signer`
- Add sourcePath to the MSR from Application manifest (in Spec) in order to fetch governed files
- Add handling of SSH / PGP passphrases input
- Add some alias for monorepo in index file. Don't require, if there is only one entry in the index file. (It will require the mrt alias, if there is more than 1 project in the index file)
- Reset Application after MRT deletion back to the desired targetRevision
- Make Qubmango to be default signer of version 0
- Fix infinite sync loop (maybe the reason, is that we block sync and allow only the next commit. it partially synced and stopped)
- Remove Status from MSR, because it's not needed. Only latest MSR matters. Or even better move it to Status (CLI)
- Release all locks, in case of failures.
- Implement governors notification, on MCA completeness
- Update MCA documentation (create, reconcile)
- Add proactive in MRT
- Prevent ArgoCD from syncing targetRevision from repository (added `syncOptions: RespectIgnoreDifferences=true`)
- MRT / MSR should point to the exact git of the request, not the next one 


TODO: 
- Implement history overview commands
- Is it secure to save passphrases on the local computer? Maybe use env variables?
- How can we trust the key from MSR and MRT as a CLI user?
- Review all controller transactional methods. Fix / Improve them, if needed
- Split MRT (maybe MSR, MCA) deletion on sub-steps.
- Generate concat documents
- Interactive pull intervals
- Remove LastObservedCommitHash, replace with timed set, that stores old revisions
- Move controller from deployment key to Git applications. Otherwise requires the machine to trust the github host (have known_hosts in their ~/.ssh/known_hosts)
- Move all Taskfile from inside to outside (or just to the subproject root)
- What to do with MSR, MCA after MRT deletion and finish of governance process? Should we remove signatures from the repository, leave for now, because it is additional logic, leave and backup?
- Make support of any argocd namespace (what was the error?)
- What should we do with rollback to previously approved changes (MCA v_k -> MCA v_k-1)?
- Refactor queue and queue usage (now we expect queue to have only one type of events)
- GovernanceFolder cannot be changed after creation. Or think about scenario, how it could be changed. It would require: what to do with old MSRs, MCAs. Change entry in the index file on update.
- Snapshot governor signatures for old MSRs (when new MSR is created, we should snapshot the signatures for the previous MSR)
- If secrets names change - we should reestablish repository