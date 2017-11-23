package store

//store package store the world - state data
import (
	"code.aliyun.com/chain33/chain33/common"
	dbm "code.aliyun.com/chain33/chain33/common/db"
	"code.aliyun.com/chain33/chain33/queue"
	"code.aliyun.com/chain33/chain33/types"
	log "github.com/inconshreveable/log15"
)

/*
模块主要的功能：

//批量写
1. EventStoreSet(stateHash, (k1,v1),(k2,v2),(k3,v3)) -> 返回 stateHash

//批量读
2. EventStoreGet(stateHash, k1,k2,k3)

*/

var slog = log.New("module", "store")
var zeroHash [32]byte

func SetLogLevel(level string) {
	common.SetLogLevel(level)
}

func DisableLog() {
	slog.SetHandler(log.DiscardHandler())
}

type Store struct {
	db      dbm.DB
	qclient queue.IClient
}

func New() *Store {
	db := dbm.NewDB("store", "leveldb", "")
	store := &Store{db: db}
	return store
}

func (store *Store) SetQueue(q *queue.Queue) {
	store.qclient = q.GetClient()
	client := store.qclient
	client.Sub("store")

	//recv 消息的处理
	go func() {
		for msg := range client.Recv() {
			slog.Info("stroe recv", "msg", msg)
			if msg.Ty == types.EventStoreSet {
				datas := msg.GetData().(*types.StoreSet)
				batch := store.db.NewBatch(true)
				for i := 0; i < len(datas.KV); i++ {
					batch.Set(datas.KV[i].Key, datas.KV[i].Key)
				}
				batch.Write()
				//response
				msg.Reply(client.NewMessage("", types.EventStoreSetReply,
					&types.ReplyHash{zeroHash[:]}))
			} else if msg.Ty == types.EventStoreGet {
				datas := msg.GetData().(*types.StoreGet)
				var values [][]byte
				for i := 0; i < len(datas.Keys); i++ {
					value := store.db.Get(datas.Keys[i])
					values = append(values, value)
				}
				msg.Reply(client.NewMessage("", types.EventStoreGetReply,
					&types.StoreReplyValue{values}))
			}
		}
	}()
}