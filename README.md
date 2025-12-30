# quorum-based-manifests-governance

TODO: 
1. Move controller from deployment key to Git applications. Otherwise requires the machine to trust the github host (have known_hosts in their ~/.ssh/known_hosts)
2. Transactions
3. Remove index from file on MRT deletion (Done)
4. Point ArgoCD on specific commit after MCA is created
5. Move all Taskfile from inside to outside (or just to the subproject root)
6. Old / multiple revisions
7. ArgoCD Application should have ignoreDifferences for targetRevision field. We wont implement any automation so far.
8. Maybe attach all events to queue and make queue owner of events (SetControllerReference)