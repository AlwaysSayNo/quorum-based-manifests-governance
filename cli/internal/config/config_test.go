package config_test

import (
	"os"
	"path/filepath"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	. "github.com/AlwaysSayNo/quorum-based-manifests-governance/cli/internal/config"

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
			
			// Verify that the file was actually created on disk
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
	
	Context("when the config file exists with valid data", func() {
		It("should correctly unmarshal the data", func() {
			// SETUP
			yamlContent := "{user: {name: John Doe, email: john.doe@example.com}, currentRepository: my-repo, repositories: [{alias: my-repo, url: 'git@github.com:test/repo.git'}]}"
			Expect(os.WriteFile(configPath, []byte(yamlContent), 0644)).To(Succeed())
			
			// ACT
			cfg, err := LoadConfigWithPath(configPath)
			
			// VERIFY
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg).NotTo(BeNil())
			
			data := cfg.GetData()
			Expect(data.User.Name).To(Equal("John Doe"))
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
			initialContent := "{repositories: [{alias: my-repo, url: url, sshKeyPath: ssh, pgpKeyPath: pgp}]}"
			Expect(os.WriteFile(configPath, []byte(initialContent), 0644)).To(Succeed())
			cfg, err := LoadConfigWithPath(configPath)
			Expect(err).NotTo(HaveOccurred())

			// ACT
			err = cfg.AddRepository("my-repo", "url", "ssh", "pgp")

			// VERIFY
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("repository with alias my-repo already exists"))
		})

		It("should add a new repository to the list and save the file", func() {
			// SETUP

			// Check empty config
			cfg, err := LoadConfigWithPath(configPath)
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.GetData().Repositories).To(BeEmpty())

			// Add the first repo
			err = cfg.AddRepository("repo1", "url1", "ssh1", "pgp1")
			Expect(err).NotTo(HaveOccurred())

			// Add a second repo
			err = cfg.AddRepository("repo2", "url2", "ssh2", "pgp2")
			Expect(err).NotTo(HaveOccurred())
			
			// VERIFY
			// Load the config from disk again to verify it was saved correctly
			reloadedCfg, err := LoadConfigWithPath(configPath)
			Expect(err).NotTo(HaveOccurred())
			repos := reloadedCfg.GetData().Repositories
			Expect(repos).To(HaveLen(2))
			Expect(repos[0].Alias).To(Equal("repo1"))
			Expect(repos[1].Alias).To(Equal("repo2"))
			Expect(repos[1].SSHKeyPath).To(Equal("ssh2"))
		})
	})

	Describe("EditRepository", func() {
		It("should return an error if the repository to edit does not exist", func() {
			// SETUP

			// Check empty config
			cfg, err := LoadConfigWithPath(configPath)
			Expect(err).NotTo(HaveOccurred())
			
			// ACT
			err = cfg.EditRepository("non-existent", "url", "ssh", "pgp")
			
			// VERIFY
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("repository with alias non-existent does not exist"))
		})
		
		It("should update the details of an existing repository", func() {
			// SETUP
			initialContent := "{repositories: [{alias: my-repo, url: url, sshKeyPath: ssh, pgpKeyPath: pgp}]}"
			Expect(os.WriteFile(configPath, []byte(initialContent), 0644)).To(Succeed())
			cfg, err := LoadConfigWithPath(configPath)
			Expect(err).NotTo(HaveOccurred())

			// ACT
			err = cfg.EditRepository("my-repo", "new-url", "new-ssh", "new-pgp")
			Expect(err).NotTo(HaveOccurred())
			
			// VERIFY
			reloadedCfg, err := LoadConfigWithPath(configPath)
			Expect(err).NotTo(HaveOccurred())
			repos := reloadedCfg.GetData().Repositories
			Expect(repos).To(HaveLen(1))
			Expect(repos[0].URL).To(Equal("new-url"))
			Expect(repos[0].SSHKeyPath).To(Equal("new-ssh"))
		})
	})
	
	Describe("RemoveRepository", func() {
		It("should return an error if the repository to remove does not exist", func() {
			// SETUP
			initialContent := "{repositories: [{alias: my-repo, url: url, sshKeyPath: ssh, pgpKeyPath: pgp}]}"
			Expect(os.WriteFile(configPath, []byte(initialContent), 0644)).To(Succeed())
			cfg, err := LoadConfigWithPath(configPath)
			Expect(err).NotTo(HaveOccurred())
			
			// ACT
			err = cfg.RemoveRepository("non-existent")
			
			// VERIFY
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("repository with alias non-existent does not exist"))
		})

		It("should remove the specified repository from the list", func() {
			// SETUP
			initialContent := "{repositories: [{alias: repo1, url: url1, sshKeyPath: ssh1, pgpKeyPath: pgp1}, {alias: repo-to-delete, url: url2, sshKeyPath: ssh2, pgpKeyPath: pgp2}, {alias: repo3, url: url3, sshKeyPath: ssh3, pgpKeyPath: pgp3}]}"
			Expect(os.WriteFile(configPath, []byte(initialContent), 0644)).To(Succeed())
			cfg, err := LoadConfigWithPath(configPath)
			Expect(err).NotTo(HaveOccurred())

			// ACT
			err = cfg.RemoveRepository("repo-to-delete")
			Expect(err).NotTo(HaveOccurred())

			// VERIFY
			reloadedCfg, err := LoadConfigWithPath(configPath)
			Expect(err).NotTo(HaveOccurred())
			repos := reloadedCfg.GetData().Repositories
			Expect(repos).To(HaveLen(2))
			Expect(repos[0].Alias).To(Equal("repo1"))
			Expect(repos[1].Alias).To(Equal("repo3"))
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
			initialContent := "{repositories: [{alias: existing-repo, url: url, sshKeyPath: ssh, pgpKeyPath: pgp}]}"
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
			initialContent := "{repositories: [{alias: existing-repo, url: url, sshKeyPath: ssh, pgpKeyPath: pgp}]}"
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