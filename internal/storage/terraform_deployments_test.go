package storage_test

import (
	"errors"
	"strings"

	"github.com/cloudfoundry/cloud-service-broker/internal/storage/storagefakes"
	"github.com/cloudfoundry/cloud-service-broker/pkg/providers/tf/workspace"

	"github.com/cloudfoundry/cloud-service-broker/internal/storage"

	"github.com/cloudfoundry/cloud-service-broker/dbservice/models"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("TerraformDeployments", func() {

	BeforeEach(func() {
		By("overriding the default FakeEncryptor to not change the json on decryption")
		encryptor = &storagefakes.FakeEncryptor{
			DecryptStub: func(bytes []byte) ([]byte, error) {
				if string(bytes) == `cannot-be-decrypted` {
					return nil, errors.New("fake decryption error")
				}
				return bytes, nil
			},
			EncryptStub: func(bytes []byte) ([]byte, error) {
				if strings.Contains(string(bytes), `cannot-be-encrypted`) {
					return nil, errors.New("fake encryption error")
				}
				return []byte(`{"encrypted":` + string(bytes) + `}`), nil
			},
		}

		store = storage.New(db, encryptor)
	})

	Describe("StoreTerraformDeployments", func() {
		It("creates the right object in the database", func() {
			err := store.StoreTerraformDeployment(storage.TerraformDeployment{
				ID: "fake-id",
				Workspace: &workspace.TerraformWorkspace{
					Modules: []workspace.ModuleDefinition{
						{
							Name: "first",
						},
					},
				},
				LastOperationType:    "create",
				LastOperationState:   "succeeded",
				LastOperationMessage: "yes!!",
			})
			Expect(err).NotTo(HaveOccurred())

			var receiver models.TerraformDeployment
			Expect(db.Find(&receiver).Error).NotTo(HaveOccurred())
			Expect(receiver.ID).To(Equal("fake-id"))
			Expect(receiver.Workspace).To(Equal([]byte(`{"encrypted":{"modules":[{"Name":"first","Definition":"","Definitions":null}],"instances":null,"tfstate":null,"transform":{"parameter_mappings":null,"parameters_to_remove":null,"parameters_to_add":null}}}`)))
			Expect(receiver.LastOperationType).To(Equal("create"))
			Expect(receiver.LastOperationState).To(Equal("succeeded"))
			Expect(receiver.LastOperationMessage).To(Equal("yes!!"))
		})

		When("encoding fails", func() {
			It("returns an error", func() {
				encryptor.EncryptReturns(nil, errors.New("bang"))

				err := store.StoreTerraformDeployment(storage.TerraformDeployment{})
				Expect(err).To(MatchError("error encoding workspace: encryption error: bang"))
			})
		})

		When("details for the instance already exist in the database", func() {
			BeforeEach(func() {
				addFakeTerraformDeployments()
			})

			It("updates the existing record", func() {
				err := store.StoreTerraformDeployment(storage.TerraformDeployment{
					ID: "fake-id-2",
					Workspace: &workspace.TerraformWorkspace{
						Modules: []workspace.ModuleDefinition{
							{
								Name: "first",
							},
						},
					},
					LastOperationType:    "create",
					LastOperationState:   "succeeded",
					LastOperationMessage: "yes!!",
				})
				Expect(err).NotTo(HaveOccurred())

				var receiver models.TerraformDeployment
				Expect(db.Where(`id = "fake-id-2"`).Find(&receiver).Error).NotTo(HaveOccurred())
				Expect(receiver.ID).To(Equal("fake-id-2"))
				Expect(receiver.Workspace).To(Equal([]byte(`{"encrypted":{"modules":[{"Name":"first","Definition":"","Definitions":null}],"instances":null,"tfstate":null,"transform":{"parameter_mappings":null,"parameters_to_remove":null,"parameters_to_add":null}}}`)))
				Expect(receiver.LastOperationType).To(Equal("create"))
				Expect(receiver.LastOperationState).To(Equal("succeeded"))
				Expect(receiver.LastOperationMessage).To(Equal("yes!!"))
			})
		})
	})

	Describe("GetTerraformDeployments", func() {
		BeforeEach(func() {
			addFakeTerraformDeployments()
		})

		It("reads the right object from the database", func() {
			r, err := store.GetTerraformDeployment("fake-id-2")
			Expect(err).NotTo(HaveOccurred())

			Expect(r.ID).To(Equal("fake-id-2"))
			Expect(r.Workspace).To(Equal(&workspace.TerraformWorkspace{Modules: []workspace.ModuleDefinition{{Name: "fake-2"}}}))
			Expect(r.LastOperationType).To(Equal("update"))
			Expect(r.LastOperationState).To(Equal("failed"))
			Expect(r.LastOperationMessage).To(Equal("too bad"))
		})

		When("decoding fails", func() {
			It("returns an error", func() {
				encryptor.DecryptReturns(nil, errors.New("bang"))

				_, err := store.GetTerraformDeployment("fake-id-1")
				Expect(err).To(MatchError(`error decoding workspace "fake-id-1": decryption error: bang`))
			})
		})

		When("nothing is found", func() {
			It("returns an error", func() {
				_, err := store.GetTerraformDeployment("not-there")
				Expect(err).To(MatchError("could not find terraform deployment: not-there"))
			})
		})
	})

	Describe("ExistsTerraformDeployments", func() {
		BeforeEach(func() {
			addFakeTerraformDeployments()
		})

		It("reads the result from the database", func() {
			Expect(store.ExistsTerraformDeployment("not-there")).To(BeFalse())
			Expect(store.ExistsTerraformDeployment("also-not-there")).To(BeFalse())
			Expect(store.ExistsTerraformDeployment("fake-id-1")).To(BeTrue())
			Expect(store.ExistsTerraformDeployment("fake-id-2")).To(BeTrue())
			Expect(store.ExistsTerraformDeployment("fake-id-3")).To(BeTrue())
		})
	})

	Describe("DeleteTerraformDeployments", func() {
		BeforeEach(func() {
			addFakeTerraformDeployments()
		})

		It("deletes from the database", func() {
			Expect(store.ExistsTerraformDeployment("fake-id-3")).To(BeTrue())

			Expect(store.DeleteTerraformDeployment("fake-id-3")).NotTo(HaveOccurred())

			Expect(store.ExistsTerraformDeployment("fake-id-3")).To(BeFalse())
		})

		It("is idempotent", func() {
			Expect(store.DeleteTerraformDeployment("not-there")).NotTo(HaveOccurred())
		})
	})
})

func addFakeTerraformDeployments() {
	Expect(db.Create(&models.TerraformDeployment{
		ID:                   "fake-id-1",
		Workspace:            []byte(`{"modules":[{"Name":"fake-1","Definition":"","Definitions":null}],"instances":null,"tfstate":null,"transform":{"parameter_mappings":null,"parameters_to_remove":null,"parameters_to_add":null}}`),
		LastOperationType:    "create",
		LastOperationState:   "succeeded",
		LastOperationMessage: "amazing",
	}).Error).NotTo(HaveOccurred())
	Expect(db.Create(&models.TerraformDeployment{
		ID:                   "fake-id-2",
		Workspace:            []byte(`{"modules":[{"Name":"fake-2","Definition":"","Definitions":null}],"instances":null,"tfstate":null,"transform":{"parameter_mappings":null,"parameters_to_remove":null,"parameters_to_add":null}}`),
		LastOperationType:    "update",
		LastOperationState:   "failed",
		LastOperationMessage: "too bad",
	}).Error).NotTo(HaveOccurred())
	Expect(db.Create(&models.TerraformDeployment{
		ID:                   "fake-id-3",
		Workspace:            []byte(`{"modules":[{"Name":"fake-3","Definition":"","Definitions":null}],"instances":null,"tfstate":null,"transform":{"parameter_mappings":null,"parameters_to_remove":null,"parameters_to_add":null}}`),
		LastOperationType:    "update",
		LastOperationState:   "succeeded",
		LastOperationMessage: "great",
	}).Error).NotTo(HaveOccurred())
}
