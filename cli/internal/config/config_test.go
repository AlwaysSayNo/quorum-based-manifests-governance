package config_test

import (
	"os"
	"path/filepath"
	"testing"

	. "github.com/AlwaysSayNo/quorum-based-manifests-governance/cli/internal/config"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestConfig(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Config Suite")
}

var _ = Describe("Config Loading", func() {
	var (
		configDir  string
		configPath string
	)

	BeforeEach(func() {
		configDir = GinkgoT().TempDir()
		configPath = filepath.Join(configDir, "config.yaml")
	})

	Context("when the config path has an incorrect extension", func() {
		It("should return an error", func() {
			// SETUP
			badPath := filepath.Join(configDir, "config.txt")

			// ACT
			cfg, err := LoadConfigWithPath(badPath)

			// VERIFY
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("supports only .yaml format"))
			Expect(cfg).To(BeNil())
		})
	})

	Context("when the config file does not exist", func() {
		It("should create the file and return an empty config", func() {
			// SETUP

			// ACT
			cfg, err := LoadConfigWithPath(configPath)

			// VERIFY
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg).NotTo(BeNil())
			Expect(cfg.GetData()).To(Equal(ConfigData{}))

			_, err = os.Stat(configPath)
			Expect(os.IsNotExist(err)).To(BeFalse(), "The config file should have been created")
		})
	})

	Context("when the config file exists but is empty", func() {
		It("should parse it and return an empty config", func() {
			// SETUP
			Expect(os.WriteFile(configPath, []byte(""), 0644)).To(Succeed())

			// ACT
			cfg, err := LoadConfigWithPath(configPath)

			// VERIFY
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg).NotTo(BeNil())
			Expect(cfg.GetData()).To(Equal(ConfigData{}))
		})
	})

	Context("when the config file contains invalid yaml", func() {
		It("should return a parse error", func() {
			// SETUP
			Expect(os.WriteFile(configPath, []byte("{invalid:"), 0644)).To(Succeed())

			// ACT
			cfg, err := LoadConfigWithPath(configPath)

			// VERIFY
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("could not unmarshal"))
			Expect(cfg).To(BeNil())
		})
	})

	Context("when the config file exists with valid data", func() {
		It("should correctly unmarshal the data", func() {
			// SETUP
			yamlContent := "{user: {name: John Doe, email: john.doe@example.com}, currentRepository: my-repo, repositories: [{alias: my-repo, url: 'git@github.com:test/repo.git', sshKeyPath: /keys/id_rsa, pgpKeyPath: /keys/id_rsa.pgp, governancePublicKey: pub, governanceFolderPath: /governance, msrName: msr, mcaName: mca}]}"
			Expect(os.WriteFile(configPath, []byte(yamlContent), 0644)).To(Succeed())

			// ACT
			cfg, err := LoadConfigWithPath(configPath)

			// VERIFY
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg).NotTo(BeNil())

			data := cfg.GetData()
			Expect(data.User.Name).To(Equal("John Doe"))
			Expect(data.User.Email).To(Equal("john.doe@example.com"))
			Expect(data.CurrentRepository).To(Equal("my-repo"))
			Expect(data.Repositories).To(HaveLen(1))
			Expect(data.Repositories[0].Alias).To(Equal("my-repo"))
		})
	})
})

var _ = Describe("Config Modification", func() {
	var (
		configDir  string
		configPath string
	)

	BeforeEach(func() {
		configDir = GinkgoT().TempDir()
		configPath = filepath.Join(configDir, "config.yaml")
	})

	Describe("AddRepository", func() {
		It("should return an error if a repository with the same alias already exists", func() {
			// SETUP
			initialContent := "{repositories: [{alias: my-repo, url: url, sshKeyPath: ssh, pgpKeyPath: pgp, governancePublicKey: pub, governanceFolderPath: /governance, msrName: msr, mcaName: mca}]}"
			Expect(os.WriteFile(configPath, []byte(initialContent), 0644)).To(Succeed())
			cfg, err := LoadConfigWithPath(configPath)
			Expect(err).NotTo(HaveOccurred())

			// ACT
			err = cfg.AddRepository(GitRepository{Alias: "my-repo", URL: "url"})

			// VERIFY
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("repository with alias my-repo already exists"))
		})

		It("should add new repositories and persist them", func() {
			// SETUP
			cfg, err := LoadConfigWithPath(configPath)
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.GetData().Repositories).To(BeEmpty())

			repo1 := GitRepository{
				Alias:                "repo1",
				URL:                  "url1",
				SSHKeyPath:           "/keys/repo1",
				PGPKeyPath:           "/keys/repo1.pgp",
				GovernancePublicKey:  "pub1",
				GovernanceFolderPath: "/governance/repo1",
				MSRName:              "msr1",
				MCAName:              "mca1",
			}
			repo2 := GitRepository{Alias: "repo2", URL: "url2"}

			// ACT
			Expect(cfg.AddRepository(repo1)).To(Succeed())
			Expect(cfg.AddRepository(repo2)).To(Succeed())

			// VERIFY
			reloadedCfg, err := LoadConfigWithPath(configPath)
			Expect(err).NotTo(HaveOccurred())
			repos := reloadedCfg.GetData().Repositories
			Expect(repos).To(HaveLen(2))
			Expect(repos[0].Alias).To(Equal("repo1"))
			Expect(repos[0].GovernanceFolderPath).To(Equal("/governance/repo1"))
			Expect(repos[1].Alias).To(Equal("repo2"))
		})
	})

	Describe("EditRepository", func() {
		It("should return an error if the repository to edit does not exist", func() {
			// SETUP
			cfg, err := LoadConfigWithPath(configPath)
			Expect(err).NotTo(HaveOccurred())

			// ACT
			err = cfg.EditRepository(GitRepository{Alias: "non-existent", URL: "url"})

			// VERIFY
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("repository with alias non-existent does not exist"))
		})

		It("should update only provided fields and keep existing values", func() {
			// SETUP
			initialContent := "{repositories: [{alias: my-repo, url: url, sshKeyPath: ssh, pgpKeyPath: pgp, governancePublicKey: pub, governanceFolderPath: /governance, msrName: msr, mcaName: mca}]}"
			Expect(os.WriteFile(configPath, []byte(initialContent), 0644)).To(Succeed())
			cfg, err := LoadConfigWithPath(configPath)
			Expect(err).NotTo(HaveOccurred())

			// ACT
			err = cfg.EditRepository(GitRepository{Alias: "my-repo", URL: "new-url", MCAName: "new-mca"})
			Expect(err).NotTo(HaveOccurred())

			// VERIFY
			reloadedCfg, err := LoadConfigWithPath(configPath)
			Expect(err).NotTo(HaveOccurred())
			repos := reloadedCfg.GetData().Repositories
			Expect(repos).To(HaveLen(1))
			Expect(repos[0].URL).To(Equal("new-url"))
			Expect(repos[0].MCAName).To(Equal("new-mca"))
			Expect(repos[0].SSHKeyPath).To(Equal("ssh"))
			Expect(repos[0].GovernanceFolderPath).To(Equal("/governance"))
		})
	})

	Describe("RemoveRepository", func() {
		It("should return an error if the repository to remove does not exist", func() {
			// SETUP
			initialContent := "{repositories: [{alias: my-repo, url: url}]}"
			Expect(os.WriteFile(configPath, []byte(initialContent), 0644)).To(Succeed())
			cfg, err := LoadConfigWithPath(configPath)
			Expect(err).NotTo(HaveOccurred())

			// ACT
			err = cfg.RemoveRepository("non-existent")

			// VERIFY
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("repository with alias non-existent does not exist"))
		})

		It("should remove the repository and clear current selection if needed", func() {
			// SETUP
			initialContent := "{currentRepository: repo-to-delete, repositories: [{alias: repo1, url: url1}, {alias: repo-to-delete, url: url2}, {alias: repo3, url: url3}]}"
			Expect(os.WriteFile(configPath, []byte(initialContent), 0644)).To(Succeed())
			cfg, err := LoadConfigWithPath(configPath)
			Expect(err).NotTo(HaveOccurred())

			// ACT
			Expect(cfg.RemoveRepository("repo-to-delete")).To(Succeed())

			// VERIFY
			reloadedCfg, err := LoadConfigWithPath(configPath)
			Expect(err).NotTo(HaveOccurred())
			repos := reloadedCfg.GetData().Repositories
			Expect(repos).To(HaveLen(2))
			Expect(repos[0].Alias).To(Equal("repo1"))
			Expect(repos[1].Alias).To(Equal("repo3"))
			Expect(reloadedCfg.GetData().CurrentRepository).To(BeEmpty())
		})
	})

	Describe("SetUser", func() {
		It("should set the user name and email in the config", func() {
			// SETUP
			cfg, err := LoadConfigWithPath(configPath)
			Expect(err).NotTo(HaveOccurred())

			// ACT
			err = cfg.SetUser("Jane Doe", "jane.doe@example.com")
			Expect(err).NotTo(HaveOccurred())

			// VERIFY
			reloadedCfg, err := LoadConfigWithPath(configPath)
			Expect(err).NotTo(HaveOccurred())
			user := reloadedCfg.GetData().User
			Expect(user.Name).To(Equal("Jane Doe"))
			Expect(user.Email).To(Equal("jane.doe@example.com"))
		})
	})
})

var _ = Describe("Config State Management", func() {
	var (
		configDir  string
		configPath string
	)

	BeforeEach(func() {
		configDir = GinkgoT().TempDir()
		configPath = filepath.Join(configDir, "config.yaml")
	})

	Describe("UseRepository", func() {
		It("should return an error if a non-empty alias does not exist", func() {
			// SETUP
			initialContent := "{repositories: [{alias: existing-repo, url: url}]}"
			Expect(os.WriteFile(configPath, []byte(initialContent), 0644)).To(Succeed())
			cfg, err := LoadConfigWithPath(configPath)
			Expect(err).NotTo(HaveOccurred())

			// ACT
			err = cfg.UseRepository("non-existent-repo")

			// VERIFY
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("repository with alias non-existent-repo does not exist"))
		})

		It("should set the CurrentRepository field and save the file", func() {
			// SETUP
			initialContent := "{currentRepository: repo1, repositories: [{alias: repo1}, {alias: repo2}]}"
			Expect(os.WriteFile(configPath, []byte(initialContent), 0644)).To(Succeed())
			cfg, err := LoadConfigWithPath(configPath)
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.GetData().CurrentRepository).To(Equal("repo1"))

			// ACT
			err = cfg.UseRepository("repo2")
			Expect(err).NotTo(HaveOccurred())

			// VERIFY
			reloadedCfg, err := LoadConfigWithPath(configPath)
			Expect(err).NotTo(HaveOccurred())
			Expect(reloadedCfg.GetData().CurrentRepository).To(Equal("repo2"))
		})

		It("should unset the CurrentRepository field when an empty alias is provided", func() {
			// SETUP
			initialContent := "{currentRepository: repo1, repositories: [{alias: repo1}]}"
			Expect(os.WriteFile(configPath, []byte(initialContent), 0644)).To(Succeed())
			cfg, err := LoadConfigWithPath(configPath)
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.GetData().CurrentRepository).To(Equal("repo1"))

			// ACT
			err = cfg.UseRepository("")
			Expect(err).NotTo(HaveOccurred())

			// VERIFY
			reloadedCfg, err := LoadConfigWithPath(configPath)
			Expect(err).NotTo(HaveOccurred())
			Expect(reloadedCfg.GetData().CurrentRepository).To(BeEmpty())
		})
	})

	Describe("GetRepository", func() {
		It("should return an error if the requested repository alias does not exist", func() {
			// SETUP
			initialContent := "{repositories: [{alias: existing-repo, url: url}]}"
			Expect(os.WriteFile(configPath, []byte(initialContent), 0644)).To(Succeed())
			cfg, err := LoadConfigWithPath(configPath)
			Expect(err).NotTo(HaveOccurred())

			// ACT
			repo, err := cfg.GetRepository("non-existent-repo")

			// VERIFY
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("repository with alias non-existent-repo does not exist"))
			Expect(repo).To(Equal(GitRepository{}))
		})

		It("should return the correct repository struct when the alias exists", func() {
			// SETUP
			initialContent := "{repositories: [{alias: repo1, url: url1, sshKeyPath: /path/to/ssh1, pgpKeyPath: /path/to/pgp1}, {alias: repo2, url: url2, sshKeyPath: /path/to/ssh2, pgpKeyPath: /path/to/pgp2}]}"
			Expect(os.WriteFile(configPath, []byte(initialContent), 0644)).To(Succeed())
			cfg, err := LoadConfigWithPath(configPath)
			Expect(err).NotTo(HaveOccurred())

			// ACT
			repo, err := cfg.GetRepository("repo2")

			// VERIFY
			Expect(err).NotTo(HaveOccurred())
			Expect(repo.Alias).To(Equal("repo2"))
			Expect(repo.URL).To(Equal("url2"))
			Expect(repo.SSHKeyPath).To(Equal("/path/to/ssh2"))
			Expect(repo.PGPKeyPath).To(Equal("/path/to/pgp2"))
		})
	})
})
