package types

import (
	"bytes"
	"encoding/hex"
	"fmt"

	"github.com/golang/protobuf/proto"
	log "github.com/inconshreveable/log15"
	"gitlab.33.cn/chain33/chain33/common"
	"gitlab.33.cn/chain33/chain33/common/address"


	_ "gitlab.33.cn/chain33/chain33/common/crypto/ed25519"
	_ "gitlab.33.cn/chain33/chain33/common/crypto/secp256k1"
)

var tlog = log.New("module", "types")

type Message proto.Message

var userKey = []byte("user.")
var slash = []byte("-")

//交易组的接口，Transactions 和 Transaction 都符合这个接口
type TxGroup interface {
	Tx() *Transaction
	GetTxGroup() (*Transactions, error)
	CheckSign() bool
}

func IsAllowExecName(name string) bool {
	return isAllowExecName([]byte(name))
}

func isAllowExecName(name []byte) bool {
	// name长度不能超过系统限制
	if len(name) > address.MaxExecNameLength {
		return false
	}
	// name中不允许有 "-"
	if bytes.Contains(name, slash) {
		return false
	}
	if bytes.HasPrefix(name, userKey) {
		return true
	}
	for i := range AllowUserExec {
		if bytes.Equal(AllowUserExec[i], name) {
			return true
		}
	}
	return false
}

func Encode(data proto.Message) []byte {
	b, err := proto.Marshal(data)
	if err != nil {
		panic(err)
	}
	return b
}

func Size(data proto.Message) int {
	return proto.Size(data)
}

func Decode(data []byte, msg proto.Message) error {
	return proto.Unmarshal(data, msg)
}

func (leafnode *LeafNode) Hash() []byte {
	data, err := proto.Marshal(leafnode)
	if err != nil {
		panic(err)
	}
	return common.Sha256(data)
}

func (innernode *InnerNode) Hash() []byte {
	data, err := proto.Marshal(innernode)
	if err != nil {
		panic(err)
	}
	return common.Sha256(data)
}

func NewErrReceipt(err error) *Receipt {
	berr := err.Error()
	errlog := &ReceiptLog{TyLogErr, []byte(berr)}
	return &Receipt{ExecErr, nil, []*ReceiptLog{errlog}}
}

func CheckAmount(amount int64) bool {
	if amount <= 0 || amount >= MaxCoin {
		return false
	}
	return true
}

func GetEventName(event int) string {
	name, ok := eventName[event]
	if ok {
		return name
	}
	return "unknow-event"
}

func GetSignatureTypeName(signType int) string {
	if signType == 1 {
		return "secp256k1"
	} else if signType == 2 {
		return "ed25519"
	} else if signType == 3 {
		return "sm2"
	} else {
		return "unknow"
	}
}

var ConfigPrefix = "mavl-config-"

func ConfigKey(key string) string {
	return fmt.Sprintf("%s-%s", ConfigPrefix, key)
}

var ManagePrefix = "mavl-manage"

func ManageKey(key string) string {
	return fmt.Sprintf("%s-%s", ManagePrefix, key)
}

func ManaeKeyWithHeigh(key string, height int64) string {
	if height >= ForkV13ExecKey {
		return ManageKey(key)
	} else {
		return ConfigKey(key)
	}
}

type ReceiptDataResult struct {
	Ty     int32               `json:"ty"`
	TyName string              `json:"tyname"`
	Logs   []*ReceiptLogResult `json:"logs"`
}

type ReceiptLogResult struct {
	Ty     int32       `json:"ty"`
	TyName string      `json:"tyname"`
	Log    interface{} `json:"log"`
	RawLog string      `json:"rawlog"`
}

func (r *ReceiptData) DecodeReceiptLog() (*ReceiptDataResult, error) {
	result := &ReceiptDataResult{Ty: r.GetTy()}
	switch r.Ty {
	case 0:
		result.TyName = "ExecErr"
	case 1:
		result.TyName = "ExecPack"
	case 2:
		result.TyName = "ExecOk"
	default:
		return nil, ErrLogType
	}
	logs := r.GetLogs()
	for _, l := range logs {
		var lTy string
		var logIns interface{}
		lLog, err := hex.DecodeString(common.ToHex(l.GetLog())[2:])
		if err != nil {
			return nil, err
		}
		// TODO
		logType := LoadLog(int64(l.Ty))
		if logType == nil {
			return nil, ErrLogType
		}
		logIns, err = logType.Decode(lLog)
		lTy = logType.Name()
		//decode, err := LoadLogDecode(l.Ty)
		//decode.Decode(lLog)
		//decode.Name()
		switch l.Ty {
		case TyLogExecTransfer:
			lTy = "LogExecTransfer"
			var logTmp ReceiptExecAccountTransfer
			err = Decode(lLog, &logTmp)
			if err != nil {
				return nil, err
			}
			logIns = logTmp
		case TyLogExecWithdraw:
			lTy = "LogExecWithdraw"
			var logTmp ReceiptExecAccountTransfer
			err = Decode(lLog, &logTmp)
			if err != nil {
				return nil, err
			}
			logIns = logTmp
		case TyLogExecDeposit:
			lTy = "LogExecDeposit"
			var logTmp ReceiptExecAccountTransfer
			err = Decode(lLog, &logTmp)
			if err != nil {
				return nil, err
			}
			logIns = logTmp
		case TyLogExecFrozen:
			lTy = "LogExecFrozen"
			var logTmp ReceiptExecAccountTransfer
			err = Decode(lLog, &logTmp)
			if err != nil {
				return nil, err
			}
			logIns = logTmp
		case TyLogExecActive:
			lTy = "LogExecActive"
			var logTmp ReceiptExecAccountTransfer
			err = Decode(lLog, &logTmp)
			if err != nil {
				return nil, err
			}
			logIns = logTmp
		case TyLogGenesisTransfer:
			lTy = "LogGenesisTransfer"
			var logTmp ReceiptAccountTransfer
			err = Decode(lLog, &logTmp)
			if err != nil {
				return nil, err
			}
			logIns = logTmp
		case TyLogGenesisDeposit:
			lTy = "LogGenesisDeposit"
			var logTmp ReceiptExecAccountTransfer
			err = Decode(lLog, &logTmp)
			if err != nil {
				return nil, err
			}
			logIns = logTmp
		case TyLogNewTicket:
			lTy = "LogNewTicket"
			var logTmp ReceiptTicket
			err = Decode(lLog, &logTmp)
			if err != nil {
				return nil, err
			}
			logIns = logTmp
		case TyLogCloseTicket:
			lTy = "LogCloseTicket"
			var logTmp ReceiptTicket
			err = Decode(lLog, &logTmp)
			if err != nil {
				return nil, err
			}
			logIns = logTmp
		case TyLogMinerTicket:
			lTy = "LogMinerTicket"
			var logTmp ReceiptTicket
			err = Decode(lLog, &logTmp)
			if err != nil {
				return nil, err
			}
			logIns = logTmp
		case TyLogTicketBind:
			lTy = "LogTicketBind"
			var logTmp ReceiptTicketBind
			err = Decode(lLog, &logTmp)
			if err != nil {
				return nil, err
			}
			logIns = logTmp

		case TyLogCallContract:
			lTy = "LogCallContract"
			var logTmp ReceiptEVMContract
			err = Decode(lLog, &logTmp)
			if err != nil {
				return nil, err
			}
			logIns = logTmp
		case TyLogContractData:
			lTy = "LogContractData"
			var logTmp EVMContractData
			err = Decode(lLog, &logTmp)
			if err != nil {
				return nil, err
			}
			logIns = logTmp
		case TyLogContractState:
			lTy = "LogContractState"
			var logTmp EVMContractState
			err = Decode(lLog, &logTmp)
			if err != nil {
				return nil, err
			}
			logIns = logTmp
		default:
			//log.Error("DecodeLog", "Faile to decodeLog with type value:%d", l.Ty)
			return nil, ErrLogType
		}
		result.Logs = append(result.Logs, &ReceiptLogResult{Ty: l.Ty, TyName: lTy, Log: logIns, RawLog: common.ToHex(l.GetLog())})
	}
	return result, nil
}

func (r *ReceiptData) OutputReceiptDetails(logger log.Logger) {
	rds, err := r.DecodeReceiptLog()
	if err == nil {
		logger.Debug("receipt decode", "receipt data", rds)
		for _, rdl := range rds.Logs {
			logger.Debug("receipt log", "log", rdl)
		}
	} else {
		logger.Error("decodelogerr", "err", err)
	}
}

func (t *ReplyGetTotalCoins) IterateRangeByStateHash(key, value []byte) bool {
	//tlog.Debug("ReplyGetTotalCoins.IterateRangeByStateHash", "key", string(key), "value", string(value))
	var acc Account
	err := Decode(value, &acc)
	if err != nil {
		tlog.Error("ReplyGetTotalCoins.IterateRangeByStateHash", "err", err)
		return true
	}
	//tlog.Info("acc:", "value", acc)
	if t.Num >= t.Count {
		t.NextKey = key
		return true
	}
	t.Num++
	t.Amount += acc.Balance
	return false
}








