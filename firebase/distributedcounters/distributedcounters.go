package distributedcounters

import (
	"cloud.google.com/go/firestore"
	"context"
	"errors"
	"fmt"
	"github.com/sabriboughanmi/go_utils/utils"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"log"
	"math/rand"
	"strconv"
	"time"
)

//DistributedCounters used to initialize Distributed Counters
type DistributedCounters struct {
	ShardCount            int
	ShardName             string
	ShardDefaultStructure interface{}
	RollUpTime            int64 //how many seconds before the next rollup
}

const (
	documentID   ShardField = "did" //This field is used to Track/Order Shards by their Parent Document for roll-up Process
	creationTick ShardField = "ct"  // This Field is used to Track which shard has exceeded to rollup time
)

var (
	internalKeys = []ShardField{documentID, creationTick}
)

//Return if a string is an internal field
func isInternalFields(field ShardField) bool {
	for _, key := range internalKeys {
		if key == field {
			return true
		}
	}
	return false
}

//Panic if the key is an internal ShardField
func checkInternalFieldsUsage(field ShardField) {
	for _, key := range internalKeys {
		if key == field {
			panic(fmt.Sprintf("DistributedCounters Package do not Allow the usage of internal Keys: %v", internalKeys))
		}
	}
}

var (
	//Errors
	NoShardFieldSpecified = errors.New("no Shard Fields Specified")
)

type ShardField string

type DistributedShard map[ShardField]interface{}

//DistributedCounterInstance is a collection of documents (shards)
//to realize counter with high frequency.
//This Struct will be created by every Incremental Section (Videos Likes, Comments Likes ..)
type DistributedCounterInstance struct {
	shardName             string
	numShards             int
	shardFields           DistributedShard
	defaultShardStructure interface{}
	rollUpTime            int64
}

//RollUp Shards of a specific Document,
//Warning! If an array of DocumentSnapshots is passed with multiple parents the first parent will get updated by all Shards
func rollUpShards(client *firestore.Client, ctx context.Context, shards ...*firestore.DocumentSnapshot) error {
	if shards == nil || len(shards) == 0 {
		return fmt.Errorf("no documents to process")
	}

	batch := client.Batch()

	//Collect Data from Shards
	incrementalFields := make(map[string]interface{})
	for i := 0; i < len(shards); i++ {

		//Cache the doc for performance reasons
		doc := shards[i]


		//Collect Data
		dataMap := doc.Data()

		for key, value := range dataMap {
			//Skip internal Keys
			if isInternalFields(ShardField(key)) {
				continue
			}

			switch value.(type) {
			case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
				if incrementalFields[key] == nil {
					incrementalFields[key] = value.(int64)
					continue
				}
				incrementalFields[key] = incrementalFields[key].(int64) + value.(int64)
				break

			case float32, float64:
				if incrementalFields[key] == nil {
					incrementalFields[key] = value.(float64)
					continue
				}
				incrementalFields[key] = incrementalFields[key].(float64) + value.(float64)
				break

			default:
				//Ignore Key
				continue
			}
		}

		//Add to delete batch
		batch.Delete(doc.Ref)
	}

	valuesToUpdate := make([]firestore.Update, 0)
	for key, value := range incrementalFields {
		//Update only non zeroed Values
		if value != 0 {
			valuesToUpdate = append(valuesToUpdate, firestore.Update{
				Path:  key,
				Value: firestore.Increment(value),
			})
		}
	}

	//Skip Update if Values are
	if len(valuesToUpdate) == 0 {
		return nil
	}

	//Update Fields in Parent document
	_, err := shards[0].Ref.Parent.Parent.Update(ctx, valuesToUpdate)
	if err != nil {
		return err
	}

	//Delete Shards
	_, err = batch.Commit(ctx)
	return err
}

//RollUP all documents Shards relative to the DistributedCounters.ShardName
func (dc *DistributedCounters) RollUp(client *firestore.Client, ctx context.Context) error {

	currentTick := time.Now().Unix() / dc.RollUpTime
	ticks := make([]int64, 10)
	var i int64
	for i = 0; i < 10; i++ {
		ticks[i] = currentTick - i
	}

	//Loop Managers
	var cursor *firestore.DocumentSnapshot = nil
	var shardsInQueue []*firestore.DocumentSnapshot
	var moreShardsExists = true

	for moreShardsExists {
		query := client.CollectionGroup(dc.ShardName).OrderBy(string(documentID), firestore.Asc).Where(string(creationTick), "in", ticks).Limit(dc.ShardCount)
		if cursor != nil {
			query.StartAfter(cursor)
		}
		it := query.Documents(ctx)
		newShards, err := it.GetAll()
		if err != nil {
			return err
		}

		//Prepare Exit/Cursor
		if len(newShards) < dc.ShardCount {
			moreShardsExists = false
		} else {
			cursor = newShards[len(newShards)-1]
		}

		//Append new Shards
		shardsInQueue = append(shardsInQueue, newShards...)

		firstElementToProcess := 0
		//Process Shards Queue
		for i := 0; i < len(shardsInQueue); i++ {
			//Last Shard in Queue
			if i+1 == len(shardsInQueue) {
				if moreShardsExists {
					//Remove Processed Shards from shardsInQueue
					shardsInQueue = shardsInQueue[firstElementToProcess : i+1]
					break
				}

				//Process Remaining Shards and quit
				return rollUpShards(client, ctx, shardsInQueue[firstElementToProcess:i+1]...)
			}

			//Skip if Parent Still Same
			if shardsInQueue[i] == shardsInQueue[i+1] {
				continue
			}

			//Shard Parent Changed
			//Process Shards
			err = rollUpShards(client, ctx, shardsInQueue[firstElementToProcess:i+1]...)
			if err != nil {
				log.Fatal(err)
			}
			firstElementToProcess = i + 1
		}
	}

	return nil
}

//CreateDistributedCounter returns a CreateDistributedCounter to manage Shards
func (dc *DistributedCounters) CreateDistributedCounter() DistributedCounterInstance {
	return DistributedCounterInstance{
		shardName:             dc.ShardName,
		numShards:             dc.ShardCount,
		shardFields:           make(map[ShardField]interface{}),
		defaultShardStructure: dc.ShardDefaultStructure,
		rollUpTime:            dc.RollUpTime,
	}
}

/* Non support for non incremental Fields
//CreateField Adds a Shard.Field for updated
func (c *DistributedCounterInstance) CreateField(field ShardField, value interface{}) {
	checkInternalFieldsUsage(field)
	c.shardFields[field] = value
}*/

//Increments a Shard.Field for updated.
//Note! The Shard supported values are:
//   int, int8, int16, int32, int64
//   uint8, uint16, uint32
//   float32, float64
func (c *DistributedCounterInstance) IncrementField(field ShardField, value interface{}) {
	checkInternalFieldsUsage(field)
	switch value.(type) {
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, float32, float64:
		c.shardFields[field] = firestore.Increment(value)
	default:
		panic("IncrementField supported values are: int, int8, int16, int32, int64, uint8, uint16, uint32, float32, float64")
	}
}

// CreateShards creates a given number of shards as sub-collection of the specified document.
//(This operation need to be done once per Document or it will reinitialize all shards Data )
func (c *DistributedCounterInstance) CreateShards(ctx context.Context, docRef *firestore.DocumentRef, shardData interface{}) error {
	colRef := docRef.Collection(c.shardName)

	// Initialize each shard with count=0
	for num := 0; num < c.numShards; num++ {
		if _, err := colRef.Doc(strconv.Itoa(num)).Set(ctx, shardData); err != nil {
			return err
		}
	}
	return nil
}

// UpdateCounters updates a randomly picked shard of a Document.
//If no ShardField specified, an NoShardFieldSpecified will be returned
func (c *DistributedCounterInstance) UpdateCounters(ctx context.Context, docRef *firestore.DocumentRef) (*firestore.WriteResult, error) {
	updateCount := len(c.shardFields)
	if updateCount == 0 {
		return nil, NoShardFieldSpecified
	}
	rand.Seed(time.Now().UnixNano())
	docID := strconv.Itoa(rand.Intn(c.numShards))
	shardRef := docRef.Collection(c.shardName).Doc(docID)

	//preallocate the slice for performance reasons
	updatedFields := make([]firestore.Update, updateCount+1)
	index := 0
	for key, value := range c.shardFields {
		updatedFields[index] = firestore.Update{
			Path:  string(key),
			Value: value,
		}
		index++
	}

	//Add LastUpdate for roll-up Updates
	updatedFields[updateCount] = firestore.Update{
		Path:  string(creationTick),
		Value: (time.Now().Unix() / c.rollUpTime) + 1,
	}

	wr, err := shardRef.Update(ctx, updatedFields)

	//Create New Shard if not existing (add missing Default Fields)
	if status.Code(err) == codes.NotFound {
		defaultStructure := make(map[string]interface{})

		if err = utils.InterfaceAs(c.defaultShardStructure, &defaultStructure); err != nil {
			return nil, fmt.Errorf("error mapping default shard structure for creation!: %v", err)
		}

		//Check Usage of Internal Keys
		for key := range defaultStructure {
			checkInternalFieldsUsage(ShardField(key))
		}

		//Update Fields in defaultStructure
		for i := 0; i < len(updatedFields); i++ {
			updatedField := updatedFields[i]
			defaultStructure[updatedField.Path] = updatedField.Value
		}

		//Add DocumentID for roll-up Updates
		defaultStructure[string(documentID)] = docRef.ID

		return shardRef.Set(ctx, defaultStructure)
	}

	return wr, err
}
