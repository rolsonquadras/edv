/*
Copyright SecureKey Technologies Inc. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package edvprovider

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/hyperledger/aries-framework-go/spi/storage"
	"github.com/trustbloc/edge-core/pkg/log"

	"github.com/trustbloc/edv/pkg/edvutils"
	"github.com/trustbloc/edv/pkg/restapi/messages"
	"github.com/trustbloc/edv/pkg/restapi/models"
)

const (
	logModuleName = "edv-provider"

	// VaultConfigurationStoreName is the name for the store that holds data vault configurations.
	VaultConfigurationStoreName = "data_vault_configurations"
	// VaultConfigReferenceIDTagName is the tag name used for querying vault configs based on their reference IDs.
	VaultConfigReferenceIDTagName = "ReferenceID"

	// MappingDocumentTagName is the tag name used for querying mapping documents
	// based on what attribute name they're for.
	MappingDocumentTagName = "AttributeName"
	// MappingDocumentMatchingEncryptedDocIDTagName is the tag name used for querying mapping documents
	// based on what encrypted document they're for.
	MappingDocumentMatchingEncryptedDocIDTagName = "MatchingEncryptedDocumentID"
)

var logger = log.New(logModuleName)

// errIndexNameAndValueAlreadyDeclaredUnique is returned when an attempt is made to store a document with an
// index name and value that are defined as unique in another document already. Note that depending
// on the provider implementation, it may not be guaranteed that uniqueness can always be maintained.
var errIndexNameAndValueAlreadyDeclaredUnique = errors.New("unable to store document since it contains an " +
	"index name and value that are already declared as unique in an existing document")

// errIndexNameAndValueCannotBeUnique is returned when an attempt is made to store a document with an
// index name and value that are defined as unique in the new would-be document, but another document already has
// an identical index name + value pair defined so uniqueness cannot be achieved. Note that depending
// on the provider implementation, it may not be guaranteed that uniqueness can always be maintained.
var errIndexNameAndValueCannotBeUnique = errors.New("unable to store document since it contains an " +
	"index name and value that are declared as unique, but another document already has an " +
	"identical index name + value pair")

type indexMappingDocument struct {
	AttributeName          string `json:"attributeName"`
	MatchingEncryptedDocID string `json:"matchingEncryptedDocID"`
	MappingDocumentName    string `json:"mappingDocumentName"`
}

type (
	checkIfBase58Encoded128BitValueFunc func(id string) error
	base58Encoded128BitToUUIDFunc       func(name string) (string, error)
)

// Provider represents an EDV storage provider.
// It wraps an Aries storage provider with additional functionality that's needed for EDV operations.
type Provider struct {
	coreProvider                    storage.Provider
	retrievalPageSize               uint
	checkIfBase58Encoded128BitValue checkIfBase58Encoded128BitValueFunc
	base58Encoded128BitToUUID       base58Encoded128BitToUUIDFunc
}

// NewProvider instantiates a new Provider. retrievalPageSize is used by ariesProvider for query paging.
// It may be ignored if ariesProvider doesn't support paging.
func NewProvider(ariesProvider storage.Provider, retrievalPageSize uint) *Provider {
	return &Provider{
		coreProvider:                    ariesProvider,
		retrievalPageSize:               retrievalPageSize,
		checkIfBase58Encoded128BitValue: edvutils.CheckIfBase58Encoded128BitValue,
		base58Encoded128BitToUUID:       edvutils.Base58Encoded128BitToUUID,
	}
}

// StoreExists returns a boolean indicating whether a given store has ever been created.
// It checks to see if the underlying database exists via the GetStoreConfig method.
func (c *Provider) StoreExists(name string) (bool, error) {
	storeName, err := c.determineStoreNameToUse(name)
	if err != nil {
		return false, fmt.Errorf("failed to determine store name to use: %w", err)
	}

	_, err = c.coreProvider.GetStoreConfig(storeName)
	if err != nil {
		if errors.Is(err, storage.ErrStoreNotFound) {
			return false, nil
		}

		return false, fmt.Errorf("unexpected error while getting store config: %w", err)
	}

	return true, nil
}

// OpenStore opens a store and returns it. The name is converted to a UUID if it is a base58-encoded
// 128-bit value.
func (c *Provider) OpenStore(name string) (*Store, error) {
	storeName, err := c.determineStoreNameToUse(name)
	if err != nil {
		return nil, fmt.Errorf("failed to determine store name to use: %w", err)
	}

	coreStore, err := c.coreProvider.OpenStore(storeName)
	if err != nil {
		return nil, err
	}

	return &Store{coreStore: coreStore, name: name, retrievalPageSize: c.retrievalPageSize}, nil
}

// SetStoreConfig sets the store configuration in the underlying core provider.
func (c *Provider) SetStoreConfig(name string, config storage.StoreConfiguration) error {
	storeName, err := c.determineStoreNameToUse(name)
	if err != nil {
		return fmt.Errorf("failed to determine store name to use: %w", err)
	}

	return c.coreProvider.SetStoreConfig(storeName, config)
}

// Store represents an EDV store.
// It wraps an Aries store with additional functionality that's needed for EDV operations.
type Store struct {
	coreStore         storage.Store
	name              string
	retrievalPageSize uint
}

// Put stores the given document.
// Mapping documents are also created and stored in order to allow for encrypted indices to work.
func (c *Store) Put(document models.EncryptedDocument) error {
	err := c.validateNewDocIndexAttribute(document)
	if err != nil {
		return fmt.Errorf("failure during encrypted document validation: %w", err)
	}

	return c.UpsertBulk([]models.EncryptedDocument{document})
}

// UpsertBulk stores the given documents, creating or updating them as needed.
// TODO (#171): Address encrypted index limitations of this method.
func (c *Store) UpsertBulk(documents []models.EncryptedDocument) error {
	mappingDocuments := c.createMappingDocuments(documents)

	operations := make([]storage.Operation, len(mappingDocuments)+len(documents))

	for i := 0; i < len(mappingDocuments); i++ {
		operations[i].Key = mappingDocuments[i].MappingDocumentName

		mappingDocumentBytes, errMarshal := json.Marshal(mappingDocuments[i])
		if errMarshal != nil {
			return fmt.Errorf("failed to marshal mapping document into bytes: %w", errMarshal)
		}

		logger.Debugf(`Creating mapping document in vault %s: Mapping document contents: %s`,
			c.name, mappingDocumentBytes)

		operations[i].Value = mappingDocumentBytes
		operations[i].Tags = []storage.Tag{
			{
				Name:  MappingDocumentTagName,
				Value: mappingDocuments[i].AttributeName,
			},
			{
				Name:  MappingDocumentMatchingEncryptedDocIDTagName,
				Value: mappingDocuments[i].MatchingEncryptedDocID,
			},
		}
	}

	for i := len(mappingDocuments); i < len(mappingDocuments)+len(documents); i++ {
		operations[i].Key = documents[i-len(mappingDocuments)].ID

		documentBytes, errMarshal := json.Marshal(documents[i-len(mappingDocuments)])
		if errMarshal != nil {
			return fmt.Errorf("failed to marshal encrypted document %s: %w",
				documents[i-len(mappingDocuments)].ID, errMarshal)
		}

		operations[i].Value = documentBytes
	}

	err := c.coreStore.Batch(operations)
	if err != nil {
		return fmt.Errorf("failed to store encrypted document(s) and their "+
			"associated mapping document(s): %w", err)
	}

	return nil
}

// Get fetches the document associated with the given key.
func (c *Store) Get(k string) ([]byte, error) {
	return c.coreStore.Get(k)
}

// Update updates the given document.
func (c *Store) Update(newDoc models.EncryptedDocument) error {
	err := c.validateNewDocIndexAttribute(newDoc)
	if err != nil {
		return fmt.Errorf("failure during encrypted document validation: %w", err)
	}

	err = c.updateMappingDocuments(newDoc.ID, newDoc.IndexedAttributeCollections)
	if err != nil {
		return fmt.Errorf(messages.UpdateMappingDocumentFailure, newDoc.ID, err)
	}

	newDocBytes, err := json.Marshal(newDoc)
	if err != nil {
		return err
	}

	return c.coreStore.Put(newDoc.ID, newDocBytes)
}

// Delete deletes the given document and its mapping document(s).
func (c *Store) Delete(docID string) error {
	mappingDocs, err := c.getMappingDocuments(fmt.Sprintf("%s:%s",
		MappingDocumentMatchingEncryptedDocIDTagName, docID))
	if err != nil {
		return fmt.Errorf("failed to get mapping documents: %w", err)
	}

	for _, mappingDoc := range mappingDocs {
		err := c.deleteMappingDocument(mappingDoc.MappingDocumentName)
		if err != nil {
			return fmt.Errorf(messages.DeleteMappingDocumentFailure, err)
		}
	}

	return c.coreStore.Delete(docID)
}

// Query does an EDV encrypted index query.
// We first get the "mapping document" and then use the ID we get from that to lookup the associated encrypted document.
// Then we check that encrypted document to see if the value matches what was specified in the query.
// If query.Has is not blank, then we assume it's a "has" query,
// and so any documents with an index name matching query.Has will be returned regardless of value.
// TODO (#168): Add support for pagination (not currently in the spec).
//  The c.retrievalPageSize parameter is passed in from the startup args and could be used with pagination.
func (c *Store) Query(query *models.Query) ([]models.EncryptedDocument, error) {
	// TODO (#169): Use c.retrievalPageSize to do pagination within this method to help control the maximum amount of
	//  memory used here. Without official pagination support it won't be possible to truly cap memory usage, however.
	var indexName string
	if query.Has != "" {
		indexName = query.Has
	} else {
		indexName = query.Name
	}

	mappingDocuments, err := c.getMappingDocuments(fmt.Sprintf("%s:%s",
		MappingDocumentTagName, indexName))
	if err != nil {
		return nil, fmt.Errorf("failed to get mapping documents: %w", err)
	}

	if len(mappingDocuments) == 0 { // No documents match the query
		return nil, nil
	}

	documentIDs := getDocumentIDsFromMappingDocumentsWithoutDuplicates(mappingDocuments)

	encryptedDocsBytes, err := c.coreStore.GetBulk(documentIDs...)
	if err != nil {
		return nil, fmt.Errorf("failed to get encrypted documents containing matching attribute names: %w", err)
	}

	matchingEncryptedDocs := make([]models.EncryptedDocument, len(encryptedDocsBytes))

	for i, encryptedDocBytes := range encryptedDocsBytes {
		var matchingEncryptedDoc models.EncryptedDocument

		err = json.Unmarshal(encryptedDocBytes, &matchingEncryptedDoc)
		if err != nil {
			return nil, fmt.Errorf("failed to unmarshal matching encrypted document with ID %s: %w",
				documentIDs[i], err)
		}

		matchingEncryptedDocs[i] = matchingEncryptedDoc
	}

	if query.Value != "" {
		matchingEncryptedDocs = c.filterDocsByQuery(matchingEncryptedDocs, query)
	}

	return matchingEncryptedDocs, nil
}

// StoreDataVaultConfiguration stores the given DataVaultConfiguration and vaultID
func (c *Store) StoreDataVaultConfiguration(config *models.DataVaultConfiguration, vaultID string) error {
	err := c.checkDuplicateReferenceID(config.ReferenceID)
	if err != nil {
		return fmt.Errorf(messages.CheckDuplicateRefIDFailure, err)
	}

	configEntry := models.DataVaultConfigurationMapping{
		DataVaultConfiguration: *config,
		VaultID:                vaultID,
	}

	configBytes, err := json.Marshal(configEntry)
	if err != nil {
		return fmt.Errorf(messages.FailToMarshalConfig, err)
	}

	return c.coreStore.Put(vaultID, configBytes,
		storage.Tag{Name: VaultConfigReferenceIDTagName, Value: config.ReferenceID})
}

func (c *Store) checkDuplicateReferenceID(referenceID string) error {
	itr, err := c.coreStore.Query(fmt.Sprintf("%s:%s", VaultConfigReferenceIDTagName, referenceID))
	if err != nil {
		return err
	}

	ok, err := itr.Next()
	if err != nil {
		return err
	}

	if ok {
		return messages.ErrDuplicateVault
	}

	return nil
}

// createMappingDocuments creates documents with mappings of the encrypted index to the document that has it.
func (c *Store) createMappingDocuments(documents []models.EncryptedDocument) []indexMappingDocument {
	var mappingDocuments []indexMappingDocument

	for _, document := range documents {
		for _, indexedAttributeCollection := range document.IndexedAttributeCollections {
			for _, indexedAttribute := range indexedAttributeCollection.IndexedAttributes {
				mappingDocument := c.createMappingDocument(indexedAttribute, document.ID)
				mappingDocuments = append(mappingDocuments, *mappingDocument)
			}
		}
	}

	return mappingDocuments
}

// validateNewDocIndexAttribute tries to ensure that index name+pairs declared unique are maintained as such. Note that
// this cannot be guaranteed due to the nature of concurrent requests.
func (c *Store) validateNewDocIndexAttribute(newDoc models.EncryptedDocument) error {
	for _, newAttributeCollection := range newDoc.IndexedAttributeCollections {
		err := c.validateNewAttributeCollection(newAttributeCollection, newDoc.ID)
		if err != nil {
			return err
		}
	}

	return nil
}

func (c *Store) validateNewAttributeCollection(
	newAttributeCollection models.IndexedAttributeCollection, docID string) error {
	for _, newAttribute := range newAttributeCollection.IndexedAttributes {
		err := c.validateNewAttribute(newAttribute, docID)
		if err != nil {
			return err
		}
	}

	return nil
}

func (c *Store) validateNewAttribute(
	newAttribute models.IndexedAttribute, newDocID string) error {
	query := models.Query{
		Name:  newAttribute.Name,
		Value: newAttribute.Value,
	}

	existingDocs, err := c.Query(&query)
	if err != nil {
		return fmt.Errorf("failed to query for documents: %w", err)
	}

	err = c.validateNewAttributeAgainstDocs(existingDocs, newDocID, newAttribute)
	if err != nil {
		return err
	}

	return nil
}

func (c *Store) validateNewAttributeAgainstDocs(docs []models.EncryptedDocument, newDocID string,
	newAttribute models.IndexedAttribute) error {
	for _, doc := range docs {
		err := c.validateNewAttributeAgainstDoc(newAttribute, doc, newDocID)
		if err != nil {
			return err
		}
	}

	return nil
}

func (c *Store) validateNewAttributeAgainstDoc(newAttribute models.IndexedAttribute,
	doc models.EncryptedDocument, newDocID string) error {
	// Skip validating new attribute against attribute collections of the same document while updating.
	if doc.ID == newDocID {
		return nil
	}

	err := validateNewAttributeAgainstAttributeCollections(newAttribute, doc.IndexedAttributeCollections)
	if err != nil {
		return err
	}

	return nil
}

func validateNewAttributeAgainstAttributeCollections(newAttribute models.IndexedAttribute,
	attributeCollections []models.IndexedAttributeCollection) error {
	for _, attributeCollection := range attributeCollections {
		err := validateNewAttributeAgainstAttributeCollection(newAttribute, attributeCollection)
		if err != nil {
			return err
		}
	}

	return nil
}

func validateNewAttributeAgainstAttributeCollection(newAttribute models.IndexedAttribute,
	attributeCollection models.IndexedAttributeCollection) error {
	for _, attribute := range attributeCollection.IndexedAttributes {
		err := validateNewAttributeAgainstAttribute(newAttribute, attribute)
		if err != nil {
			return err
		}
	}

	return nil
}

func validateNewAttributeAgainstAttribute(newAttribute, attribute models.IndexedAttribute) error {
	if newAttribute.Name == attribute.Name && newAttribute.Value == attribute.Value {
		if attribute.Unique {
			return errIndexNameAndValueAlreadyDeclaredUnique
		}

		if newAttribute.Unique {
			return errIndexNameAndValueCannotBeUnique
		}
	}

	return nil
}

// createMappingDocument creates a document with a mapping of the encrypted index to the document that has it.
func (c *Store) createMappingDocument(indexedAttribute models.IndexedAttribute,
	encryptedDocID string) *indexMappingDocument {
	mappingDocumentName := encryptedDocID + "_mapping_" + indexedAttribute.Name + "-" + indexedAttribute.Value

	mapDocument := indexMappingDocument{
		AttributeName:          indexedAttribute.Name,
		MatchingEncryptedDocID: encryptedDocID,
		MappingDocumentName:    mappingDocumentName,
	}

	return &mapDocument
}

// createMappingDocument creates a document with a mapping of the encrypted index to the document that has it.
func (c *Store) createAndStoreMappingDocument(indexedAttributeName, encryptedDocID string) error {
	mappingDocumentName := encryptedDocID + "_mapping_" + uuid.New().String()

	mapDocument := indexMappingDocument{
		AttributeName:          indexedAttributeName,
		MatchingEncryptedDocID: encryptedDocID,
		MappingDocumentName:    mappingDocumentName,
	}

	documentBytes, err := json.Marshal(mapDocument)
	if err != nil {
		return err
	}

	logger.Debugf(`Creating mapping document in EDV "%s":
Name: %s,
Contents: %s`, c.name, mappingDocumentName, documentBytes)

	return c.coreStore.Put(mappingDocumentName, documentBytes, storage.Tag{
		Name:  MappingDocumentTagName,
		Value: mapDocument.AttributeName,
	}, storage.Tag{
		Name:  MappingDocumentMatchingEncryptedDocIDTagName,
		Value: mapDocument.MatchingEncryptedDocID,
	})
}

// updateMappingDocuments first queries mapping document names and indexNames with matching encrypted document ID.
// Then we delete the mapping documents belonging to indexNames that are removed from the update
// and create the mapping documents belonging to indexNames that are newly added.
func (c *Store) updateMappingDocuments(encryptedDocID string,
	newIndexedAttributeCollections []models.IndexedAttributeCollection) error {
	mappingDocuments, err := c.getMappingDocuments(fmt.Sprintf("%s:%s",
		MappingDocumentMatchingEncryptedDocIDTagName, encryptedDocID))
	if err != nil {
		return err
	}

	if err := c.checkAndCleanUpOldMappingDocuments(newIndexedAttributeCollections,
		mappingDocuments); err != nil {
		return err
	}

	return c.checkAndCreateNewMappingDocuments(encryptedDocID, newIndexedAttributeCollections, mappingDocuments)
}

// checkAndCreateNewMappingDocuments checks if an indexName from the new indexedAttributeCollections already exists
// before the update, if not, create a mapping document for it.
func (c *Store) checkAndCreateNewMappingDocuments(encryptedDocID string,
	newIndexedAttributeCollections []models.IndexedAttributeCollection, mappingDocs []indexMappingDocument) error {
	for _, newIndexedAttributeCollection := range newIndexedAttributeCollections {
		for _, newIndexAttribute := range newIndexedAttributeCollection.IndexedAttributes {
			indexNameFound := false

			for _, mappingDoc := range mappingDocs {
				if mappingDoc.AttributeName == newIndexAttribute.Name {
					indexNameFound = true
					break
				}
			}

			if !indexNameFound {
				if err := c.createAndStoreMappingDocument(newIndexAttribute.Name, encryptedDocID); err != nil {
					return err
				}
			}
		}
	}

	return nil
}

// checkAndCleanUpOldMappingDocuments checks if the existing indexNames still exist after the update and
// deletes mapping documents of those that should no longer exist.
func (c *Store) checkAndCleanUpOldMappingDocuments(
	newIndexedAttributeCollections []models.IndexedAttributeCollection, mappingDocs []indexMappingDocument) error {
	// for mappingDocName, oldIndexName := range mappingDocs {
	for _, mappingDoc := range mappingDocs {
		indexNameFound := false

		for _, newIndexedAttributeCollection := range newIndexedAttributeCollections {
			for _, newIndexedAttribute := range newIndexedAttributeCollection.IndexedAttributes {
				if mappingDoc.AttributeName == newIndexedAttribute.Name {
					indexNameFound = true
					break
				}
			}
		}

		if !indexNameFound {
			err := c.deleteMappingDocument(mappingDoc.MappingDocumentName)
			if err != nil {
				return err
			}
		}
	}

	return nil
}

func (c *Store) deleteMappingDocument(mappingDocName string) error {
	return c.coreStore.Delete(mappingDocName)
}

func (c *Store) getMappingDocuments(query string) ([]indexMappingDocument, error) {
	itr, err := c.coreStore.Query(query, storage.WithPageSize(int(c.retrievalPageSize)))
	if err != nil {
		return nil, err
	}

	moreEntries, err := itr.Next()
	if err != nil {
		return nil, fmt.Errorf("failed to get next entry from iterator: %w", err)
	}

	defer storage.Close(itr, logger)

	var mappingDocuments []indexMappingDocument

	for moreEntries {
		mappingDocumentBytes, valueErr := itr.Value()
		if valueErr != nil {
			return nil, valueErr
		}

		var mappingDocument indexMappingDocument

		err = json.Unmarshal(mappingDocumentBytes, &mappingDocument)
		if err != nil {
			return nil, fmt.Errorf("failed to unmarshal mapping document bytes: %w", err)
		}

		mappingDocuments = append(mappingDocuments, mappingDocument)

		moreEntries, err = itr.Next()
		if err != nil {
			return nil, err
		}
	}

	return mappingDocuments, nil
}

func (c *Store) filterDocsByQuery(documentsToFilter []models.EncryptedDocument,
	query *models.Query) []models.EncryptedDocument {
	var fullyMatchingDocuments []models.EncryptedDocument

	for _, document := range documentsToFilter {
		if documentMatchesQuery(document, query) {
			fullyMatchingDocuments = append(fullyMatchingDocuments, document)
		}
	}

	return fullyMatchingDocuments
}

func (c *Provider) determineStoreNameToUse(name string) (string, error) {
	storeName := name

	if c.checkIfBase58Encoded128BitValue(name) == nil {
		storeNameString, err := c.base58Encoded128BitToUUID(name)
		if err != nil {
			return "", fmt.Errorf("failed to generate UUID from base 58 encoded 128 bit name: %w", err)
		}

		storeName = storeNameString
	}

	return storeName, nil
}

func documentMatchesQuery(document models.EncryptedDocument, query *models.Query) bool {
	for _, indexedAttributeCollection := range document.IndexedAttributeCollections {
		if attributeCollectionSatisfiesQuery(indexedAttributeCollection, query) {
			return true
		}
	}

	return false
}

func attributeCollectionSatisfiesQuery(attrCollection models.IndexedAttributeCollection, query *models.Query) bool {
	for _, indexedAttribute := range attrCollection.IndexedAttributes {
		if indexedAttribute.Name == query.Name {
			if indexedAttribute.Value == query.Value {
				return true
			}
		}
	}

	return false
}

func getDocumentIDsFromMappingDocumentsWithoutDuplicates(mappingDocuments []indexMappingDocument) []string {
	documentIDsSet := make(map[string]struct{})

	for _, mappingDocument := range mappingDocuments {
		documentIDsSet[mappingDocument.MatchingEncryptedDocID] = struct{}{}
	}

	documentIDs := make([]string, len(documentIDsSet))

	var counter int

	for documentID := range documentIDsSet {
		documentIDs[counter] = documentID
		counter++
	}

	return documentIDs
}
