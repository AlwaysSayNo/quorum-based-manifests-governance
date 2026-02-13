package github

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("GitHubProviderFactory IdentifyProvider Method", func() {
	var factory GitHubProviderFactory

	Context("when the URL contains github.com", func() {
		It("should return true for HTTPS URL", func() {
			result := factory.IdentifyProvider("https://github.com/user/repo.git")
			Expect(result).To(BeTrue())
		})

		It("should return true for SSH URL", func() {
			result := factory.IdentifyProvider("git@github.com:user/repo.git")
			Expect(result).To(BeTrue())
		})

		It("should return true for URL with github.com anywhere", func() {
			result := factory.IdentifyProvider("ssh://git@github.com/user/repo.git")
			Expect(result).To(BeTrue())
		})
	})

	Context("when the URL does not contain github.com", func() {
		It("should return false for GitLab URL", func() {
			result := factory.IdentifyProvider("https://gitlab.com/user/repo.git")
			Expect(result).To(BeFalse())
		})

		It("should return false for Bitbucket URL", func() {
			result := factory.IdentifyProvider("https://bitbucket.org/user/repo.git")
			Expect(result).To(BeFalse())
		})

		It("should return false for custom git server", func() {
			result := factory.IdentifyProvider("https://git.example.com/user/repo.git")
			Expect(result).To(BeFalse())
		})

		It("should return false for empty URL", func() {
			result := factory.IdentifyProvider("")
			Expect(result).To(BeFalse())
		})
	})
})
