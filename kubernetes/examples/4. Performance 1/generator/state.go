package main

import (
	"fmt"
	"math/rand"
)

type resource struct {
	name      string
	namespace string
}

type state struct {
	namespaces []string
	deployments []resource
	configmaps  []resource

	nsCounter  int
	depCounter int
	cmCounter  int
}

func newState() *state {
	return &state{}
}

// New resource name generators

func (s *state) nextNS() string {
	s.nsCounter++
	return fmt.Sprintf("perf-ns-%d", s.nsCounter)
}

func (s *state) nextDeploy() string {
	s.depCounter++
	return fmt.Sprintf("perf-deploy-%d", s.depCounter)
}

func (s *state) nextCM() string {
	s.cmCounter++
	return fmt.Sprintf("perf-cm-%d", s.cmCounter)
}

// Getters

func (s *state) getRandomNS() string {
	if len(s.namespaces) == 0 {
		return ""
	}
	return s.namespaces[rand.Intn(len(s.namespaces))]
}

func (s *state) getRandomDeploy() (resource, bool) {
	if len(s.deployments) == 0 {
		return resource{}, false
	}
	return s.deployments[rand.Intn(len(s.deployments))], true
}

func (s *state) getRandomCM() (resource, bool) {
	if len(s.configmaps) == 0 {
		return resource{}, false
	}
	return s.configmaps[rand.Intn(len(s.configmaps))], true
}

// Setters

func (s *state) addNamespace(name string) {
	s.namespaces = append(s.namespaces, name)
}

func (s *state) addDeploy(name, ns string) {
	s.deployments = append(s.deployments, resource{name: name, namespace: ns})
}

func (s *state) addCM(name, ns string) {
	s.configmaps = append(s.configmaps, resource{name: name, namespace: ns})
}

// Removers

func removeResource(slice []resource, idx int) []resource {
	return append(slice[:idx], slice[idx+1:]...)
}

func removeString(slice []string, idx int) []string {
	return append(slice[:idx], slice[idx+1:]...)
}