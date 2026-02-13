/*
Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller_test

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	governancev1alpha1 "github.com/AlwaysSayNo/quorum-based-manifests-governance/kubernetes/api/v1alpha1"
	. "github.com/AlwaysSayNo/quorum-based-manifests-governance/kubernetes/internal/controller"
)

var _ = Describe("GovernanceQueueReconciler", func() {
	var (
		ctx           context.Context
		testNamespace *corev1.Namespace
		reconciler    *GovernanceQueueReconciler
		mrt           *governancev1alpha1.ManifestRequestTemplate
		queue         *governancev1alpha1.GovernanceQueue
		commitSHA1    string
		commitSHA2    string
		commitSHA3    string
		createEvent   func(name, commitSHA string) *governancev1alpha1.GovernanceEvent
	)

	BeforeEach(func() {
		ctx = context.Background()

		// Create a unique namespace for each test
		testNamespace = &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "test-gq-",
			},
		}
		Expect(k8sClient.Create(ctx, testNamespace)).To(Succeed())

		// Create reconciler
		reconciler = &GovernanceQueueReconciler{
			Client: k8sClient,
			Scheme: k8sClient.Scheme(),
		}

		// Sample commit SHAs
		commitSHA1 = "abc123def456abc123def456abc123def456abc1"
		commitSHA2 = "def456abc123def456abc123def456abc123de2"
		commitSHA3 = "789abc123def456abc123def456abc123def4563"

		// Create MRT
		mrt = &governancev1alpha1.ManifestRequestTemplate{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-mrt",
				Namespace: testNamespace.Name,
			},
			Spec: governancev1alpha1.ManifestRequestTemplateSpec{
				Version: 1,
				GitRepository: governancev1alpha1.GitRepository{
					SSH: governancev1alpha1.GitSSH{
						URL: "git@github.com:test/repo.git",
						SecretsRef: &governancev1alpha1.ManifestRef{
							Name:      "git-secret",
							Namespace: testNamespace.Name,
						},
					},
				},
				PGP: &governancev1alpha1.PGPPrivateKeySecret{
					SecretsRef: governancev1alpha1.ManifestRef{
						Name:      "pgp-secret",
						Namespace: testNamespace.Name,
					},
					PublicKey: "FAKE_PGP_KEY",
				},
				ArgoCD: governancev1alpha1.ArgoCD{
					Application: governancev1alpha1.ManifestRef{
						Name:      "test-app",
						Namespace: "argocd",
					},
				},
				MSR: governancev1alpha1.ManifestRef{
					Name:      "test-msr",
					Namespace: testNamespace.Name,
				},
				MCA: governancev1alpha1.ManifestRef{
					Name:      "test-mca",
					Namespace: testNamespace.Name,
				},
				Governors: governancev1alpha1.GovernorList{
					Members: []governancev1alpha1.Governor{
						{
							PublicKey: "test-governor-key",
							Alias:     "test-governor",
						},
					},
				},
				Require: governancev1alpha1.ApprovalRule{
					AtLeast: func() *int {
						v := 1
						return &v
					}(),
				},
			},
		}
		Expect(k8sClient.Create(ctx, mrt)).To(Succeed())

		// Create GovernanceQueue
		queue = &governancev1alpha1.GovernanceQueue{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-queue",
				Namespace: testNamespace.Name,
			},
			Spec: governancev1alpha1.GovernanceQueueSpec{
				MRT: governancev1alpha1.ManifestRef{
					Name:      mrt.Name,
					Namespace: mrt.Namespace,
				},
			},
		}
		Expect(k8sClient.Create(ctx, queue)).To(Succeed())

		// Update MRT to reference the queue
		Eventually(func() error {
			if err := k8sClient.Get(ctx, types.NamespacedName{Name: mrt.Name, Namespace: mrt.Namespace}, mrt); err != nil {
				return err
			}
			mrt.Status.RevisionQueueRef = governancev1alpha1.ManifestRefOptional{
				Name:      queue.Name,
				Namespace: queue.Namespace,
			}
			return k8sClient.Status().Update(ctx, mrt)
		}, 5*time.Second).Should(Succeed())

		// Helper function to create GovernanceEvents with consistent structure
		createEvent = func(name, commitSHA string) *governancev1alpha1.GovernanceEvent {
			event := &governancev1alpha1.GovernanceEvent{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name,
					Namespace: testNamespace.Name,
					Labels: map[string]string{
						QubmangoMRTUIDAnnotation: string(mrt.UID),
					},
				},
				Spec: governancev1alpha1.GovernanceEventSpec{
					Type: governancev1alpha1.EventTypeNewRevision,
					MRT: governancev1alpha1.ManifestRef{
						Name:      mrt.Name,
						Namespace: mrt.Namespace,
					},
					NewRevision: &governancev1alpha1.NewRevisionPayload{
						CommitSHA: commitSHA,
					},
				},
			}
			Expect(k8sClient.Create(ctx, event)).To(Succeed())
			return event
		}
	})

	AfterEach(func() {
		// Clean up by deleting the namespace
		Expect(k8sClient.Delete(ctx, testNamespace)).To(Succeed())
	})

	Describe("Reconcile Method", func() {
		Context("when queue exists and is empty", func() {
			It("should add a new GovernanceEvent to the queue", func() {
				// SETUP
				event1 := createEvent("event-1", commitSHA1)

				// ACT
				_, err := reconciler.Reconcile(ctx, reconcile.Request{
					NamespacedName: types.NamespacedName{
						Name:      queue.Name,
						Namespace: queue.Namespace,
					},
				})

				// VERIFY
				Expect(err).NotTo(HaveOccurred())

				// Fetch updated queue
				updatedQueue := &governancev1alpha1.GovernanceQueue{}
				Expect(k8sClient.Get(ctx, types.NamespacedName{Name: queue.Name, Namespace: queue.Namespace}, updatedQueue)).To(Succeed())

				Expect(updatedQueue.Status.Queue).To(HaveLen(1))
				Expect(updatedQueue.Status.Queue[0].Name).To(Equal("event-1"))
				Expect(updatedQueue.Status.Queue[0].UID).To(Equal(event1.UID))
			})
		})

		Context("when queue is non-empty and new GovernanceEvent with older timestamp comes", func() {
			It("should insert the event at the beginning of the queue", func() {
				// SETUP
				createEvent("event-1", commitSHA1)

				// Reconcile to add first event
				_, err := reconciler.Reconcile(ctx, reconcile.Request{
					NamespacedName: types.NamespacedName{Name: queue.Name, Namespace: queue.Namespace},
				})
				Expect(err).NotTo(HaveOccurred())

				// Wait a bit to ensure timestamp difference
				time.Sleep(100 * time.Millisecond)

				// Create second event with older timestamp
				event2 := createEvent("event-0-older", commitSHA2)

				// Fetch event2 and manually set older timestamp
				Eventually(func() error {
					if err := k8sClient.Get(ctx, types.NamespacedName{Name: event2.Name, Namespace: event2.Namespace}, event2); err != nil {
						return err
					}
					// Set timestamp to be earlier than event1
					event2.ObjectMeta.CreationTimestamp = metav1.NewTime(time.Now().Add(-1 * time.Hour))
					return k8sClient.Update(ctx, event2)
				}, 5*time.Second).Should(Succeed())

				// ACT
				_, err = reconciler.Reconcile(ctx, reconcile.Request{
					NamespacedName: types.NamespacedName{Name: queue.Name, Namespace: queue.Namespace},
				})

				// VERIFY
				Expect(err).NotTo(HaveOccurred())

				updatedQueue := &governancev1alpha1.GovernanceQueue{}
				Expect(k8sClient.Get(ctx, types.NamespacedName{Name: queue.Name, Namespace: queue.Namespace}, updatedQueue)).To(Succeed())

				Expect(updatedQueue.Status.Queue).To(HaveLen(2))
				// Event with older timestamp should be first
				Expect(updatedQueue.Status.Queue[0].Name).To(Equal("event-0-older"))
				Expect(updatedQueue.Status.Queue[1].Name).To(Equal("event-1"))
			})
		})

		Context("when queue is non-empty with one element and the GovernanceEvent is deleted", func() {
			It("should remove the event from the queue", func() {
				// SETUP
				event1 := createEvent("event-1", commitSHA1)

				// Reconcile to add event
				_, err := reconciler.Reconcile(ctx, reconcile.Request{
					NamespacedName: types.NamespacedName{Name: queue.Name, Namespace: queue.Namespace},
				})
				Expect(err).NotTo(HaveOccurred())

				// Verify event is in queue
				updatedQueue := &governancev1alpha1.GovernanceQueue{}
				Expect(k8sClient.Get(ctx, types.NamespacedName{Name: queue.Name, Namespace: queue.Namespace}, updatedQueue)).To(Succeed())
				Expect(updatedQueue.Status.Queue).To(HaveLen(1))

				// ACT
				// Delete the event
				Expect(k8sClient.Delete(ctx, event1)).To(Succeed())

				// Reconcile after deletion
				_, err = reconciler.Reconcile(ctx, reconcile.Request{
					NamespacedName: types.NamespacedName{Name: queue.Name, Namespace: queue.Namespace},
				})

				// VERIFY
				Expect(err).NotTo(HaveOccurred())

				Expect(k8sClient.Get(ctx, types.NamespacedName{Name: queue.Name, Namespace: queue.Namespace}, updatedQueue)).To(Succeed())
				Expect(updatedQueue.Status.Queue).To(BeEmpty())
			})
		})

		Context("when queue is non-empty with three elements and one GovernanceEvent is deleted", func() {
			It("should remove only the deleted event from the queue", func() {
				// SETUP
				createEvent("event-1", commitSHA1)
				time.Sleep(50 * time.Millisecond)

				event2 := createEvent("event-2", commitSHA2)
				time.Sleep(50 * time.Millisecond)

				createEvent("event-3", commitSHA3)

				// Reconcile to add all events
				_, err := reconciler.Reconcile(ctx, reconcile.Request{
					NamespacedName: types.NamespacedName{Name: queue.Name, Namespace: queue.Namespace},
				})
				Expect(err).NotTo(HaveOccurred())

				// Verify all three events are in queue
				updatedQueue := &governancev1alpha1.GovernanceQueue{}
				Expect(k8sClient.Get(ctx, types.NamespacedName{Name: queue.Name, Namespace: queue.Namespace}, updatedQueue)).To(Succeed())
				Expect(updatedQueue.Status.Queue).To(HaveLen(3))

				// ACT
				// Delete the middle event
				Expect(k8sClient.Delete(ctx, event2)).To(Succeed())

				// Reconcile after deletion
				_, err = reconciler.Reconcile(ctx, reconcile.Request{
					NamespacedName: types.NamespacedName{Name: queue.Name, Namespace: queue.Namespace},
				})

				// VERIFY
				Expect(err).NotTo(HaveOccurred())

				Expect(k8sClient.Get(ctx, types.NamespacedName{Name: queue.Name, Namespace: queue.Namespace}, updatedQueue)).To(Succeed())
				Expect(updatedQueue.Status.Queue).To(HaveLen(2))
				Expect(updatedQueue.Status.Queue[0].Name).To(Equal("event-1"))
				Expect(updatedQueue.Status.Queue[1].Name).To(Equal("event-3"))
			})
		})

		Context("when reconciling a non-existent queue", func() {
			It("should not return an error", func() {
				// ACT
				_, err := reconciler.Reconcile(ctx, reconcile.Request{
					NamespacedName: types.NamespacedName{
						Name:      "non-existent-queue",
						Namespace: testNamespace.Name,
					},
				})

				// VERIFY
				Expect(err).NotTo(HaveOccurred())
			})
		})

		Context("when events are being deleted (have DeletionTimestamp)", func() {
			It("should exclude events with DeletionTimestamp from the queue", func() {
				// SETUP
				createEvent("event-1", commitSHA1)
				time.Sleep(50 * time.Millisecond)

				event2 := createEvent("event-2", commitSHA2)

				// Reconcile to add both events
				_, err := reconciler.Reconcile(ctx, reconcile.Request{
					NamespacedName: types.NamespacedName{Name: queue.Name, Namespace: queue.Namespace},
				})
				Expect(err).NotTo(HaveOccurred())

				// Mark event2 for deletion (it will have DeletionTimestamp but may not be fully deleted yet)
				Expect(k8sClient.Delete(ctx, event2)).To(Succeed())

				// ACT
				// Reconcile - should exclude event2 even if not fully deleted
				_, err = reconciler.Reconcile(ctx, reconcile.Request{
					NamespacedName: types.NamespacedName{Name: queue.Name, Namespace: queue.Namespace},
				})

				// VERIFY
				Expect(err).NotTo(HaveOccurred())

				updatedQueue := &governancev1alpha1.GovernanceQueue{}
				Eventually(func() int {
					Expect(k8sClient.Get(ctx, types.NamespacedName{Name: queue.Name, Namespace: queue.Namespace}, updatedQueue)).To(Succeed())
					return len(updatedQueue.Status.Queue)
				}, 5*time.Second).Should(Equal(1))

				Expect(updatedQueue.Status.Queue[0].Name).To(Equal("event-1"))
			})
		})
	})
})
