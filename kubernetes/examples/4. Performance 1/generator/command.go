package main

import (
	"fmt"
	"math/rand"
	"strings"
)

const deploymentTemplate = `apiVersion: apps/v1
kind: Deployment
metadata:
  name: %s
  namespace: %s
spec:
  replicas: 1
  selector:
    matchLabels:
      app: %s
  template:
    metadata:
      labels:
        app: %s
    spec:
      containers:
      - name: busybox
        image: busybox:1.36
        command: ["sleep", "7200"]`

// 3 equal-chance sub-types (30% each): deployment, namespace, configmap
// 10% chance to read a non-existing resource to simulate cache misses and error handling
func cmdRead(s *state) string {
	state := rand.Intn(100)
	switch {
	// Deployment
	case state < 30:
		r, ok := s.getRandomDeploy()
		if !ok {
			return cmdReadNonExisting(s)
		}
		return fmt.Sprintf("kubectl get deployment %s -n %s", r.name, r.namespace)
	// Namespace
	case state < 60:
		ns := s.getRandomNS()
		if ns == "" {
			return cmdReadNonExisting(s)
		}
		return fmt.Sprintf("kubectl get namespace %s", ns)
	// Configmap
	case state < 90:
		r, ok := s.getRandomCM()
		if !ok {
			return cmdReadNonExisting(s)
		}
		return fmt.Sprintf("kubectl get configmap %s -n %s", r.name, r.namespace)
	// Non-existing
	default:
		return cmdReadNonExisting(s)
	}
}

func cmdReadNonExisting(s *state) string {
	ns := s.getRandomNS()
	if ns == "" {
		ns = "default"
	}
	return fmt.Sprintf("kubectl get configmap non-existing-resource-xyz -n %s", ns)
}

func cmdUpdate(s *state) string {
	// Pick a random existing resource and patch an annotation
	switch rand.Intn(3) {
	// Deployment
	case 0:
		r, ok := s.getRandomDeploy()
		if !ok {
			return cmdRead(s) // fallback to read if nothing to update
		}
		return fmt.Sprintf(
			`kubectl annotate deployment %s -n %s perf-test/ts="%d" --overwrite`,
			r.name, r.namespace, rand.Int63(),
		)
	// Namespace
	case 1:
		ns := s.getRandomNS()
		if ns == "" {
			return cmdRead(s)
		}
		return fmt.Sprintf(
			`kubectl annotate namespace %s perf-test/ts="%d" --overwrite`,
			ns, rand.Int63(),
		)
	// Configmap
	default:
		r, ok := s.getRandomCM()
		if !ok {
			return cmdRead(s)
		}
		return fmt.Sprintf(
			`kubectl annotate configmap %s -n %s perf-test/ts="%d" --overwrite`,
			r.name, r.namespace, rand.Int63(),
		)
	}
}

func cmdCreate(s *state) string {
	ns := s.getRandomNS()

	switch rand.Intn(3) {
	// Namespace
	case 0:
		return createNamespace(s)
	// Deployment
	case 1:
		if ns == "" {
			// Create a namespace instead
			return createNamespace(s)
		}
		name := s.nextDeploy()
		s.addDeploy(name, ns)
		manifest := fmt.Sprintf(deploymentTemplate, name, ns, name, name)
		escaped := strings.ReplaceAll(manifest, "'", "'\"'\"'")
		return fmt.Sprintf("kubectl apply -f - <<'EOF'\n%s\nEOF", escaped)
	// Configmap
	default:
		if ns == "" {
			// Create a namespace instead
			return createNamespace(s)
		}
		name := s.nextCM()
		s.addCM(name, ns)
		return fmt.Sprintf("kubectl create configmap %s -n %s --from-literal=key=value", name, ns)
	}
}

func createNamespace(s *state) string {
	name := s.nextNS()
	s.addNamespace(name)
	return fmt.Sprintf("kubectl create namespace %s", name)
}

func cmdDelete(s *state) string {
	// Collect candidates
	type candidate struct {
		kind string
		idx  int
	}
	var candidates []candidate
	for i := range s.deployments {
		candidates = append(candidates, candidate{"deploy", i})
	}
	for i := range s.configmaps {
		candidates = append(candidates, candidate{"cm", i})
	}
	// Only delete namespaces if > 1 remaining (keep at least one for other ops)
	if len(s.namespaces) > 1 {
		for i := range s.namespaces {
			candidates = append(candidates, candidate{"ns", i})
		}
	}

	if len(candidates) == 0 {
		// Nothing to delete, fallback
		return cmdRead(s)
	}

	// Pick one resource to delete at random
	c := candidates[rand.Intn(len(candidates))]
	switch c.kind {
	case "deploy":
		r := s.deployments[c.idx]
		s.deployments = removeResource(s.deployments, c.idx)
		return fmt.Sprintf("kubectl delete deployment %s -n %s --wait=false", r.name, r.namespace)
	case "cm":
		r := s.configmaps[c.idx]
		s.configmaps = removeResource(s.configmaps, c.idx)
		return fmt.Sprintf("kubectl delete configmap %s -n %s --wait=false", r.name, r.namespace)
	case "ns":
		ns := s.namespaces[c.idx]
		s.namespaces = removeString(s.namespaces, c.idx)
		
		// Also remove all resources in that namespace
		// Remove deployments
		var newDeps []resource
		for _, d := range s.deployments {
			if d.namespace != ns {
				newDeps = append(newDeps, d)
			}
		}
		s.deployments = newDeps
		// Remove configmaps
		var newCMs []resource
		for _, cm := range s.configmaps {
			if cm.namespace != ns {
				newCMs = append(newCMs, cm)
			}
		}
		s.configmaps = newCMs

		return fmt.Sprintf("kubectl delete namespace %s --wait=false", ns)
	}

	// Unreachable
	return cmdRead(s)
}
