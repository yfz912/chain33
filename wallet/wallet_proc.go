package wallet

import (
	"encoding/hex"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/golang/protobuf/proto"
	"gitlab.33.cn/chain33/chain33/account"
	"gitlab.33.cn/chain33/chain33/common"
	"gitlab.33.cn/chain33/chain33/common/address"
	"gitlab.33.cn/chain33/chain33/common/crypto"
	"gitlab.33.cn/chain33/chain33/types"
	"gitlab.33.cn/wallet/bipwallet"
)

//input:
//type ReqSignRawTx struct {
//	Addr    string
//	Privkey string
//	TxHex   string
//	Expire  string
//}
//output:
//string
//签名交易
func (wallet *Wallet) ProcSignRawTx(unsigned *types.ReqSignRawTx) (string, error) {
	wallet.mtx.Lock()
	defer wallet.mtx.Unlock()

	index := unsigned.Index
	if unsigned.GetAddr() == "" {
		return "", types.ErrNoPrivKeyOrAddr
	}

	ok, err := wallet.CheckWalletStatus()
	if !ok {
		return "", err
	}
	key, err := wallet.getPrivKeyByAddr(unsigned.GetAddr())
	if err != nil {
		return "", err
	}

	var tx types.Transaction
	bytes, err := common.FromHex(unsigned.GetTxHex())
	if err != nil {
		return "", err
	}
	err = types.Decode(bytes, &tx)
	if err != nil {
		return "", err
	}
	expire, err := time.ParseDuration(unsigned.GetExpire())
	if err != nil {
		return "", err
	}
	tx.SetExpire(expire)
	group, err := tx.GetTxGroup()
	if err != nil {
		return "", err
	}
	if group == nil {
		tx.Sign(int32(SignType), key)
		txHex := types.Encode(&tx)
		signedTx := hex.EncodeToString(txHex)
		return signedTx, nil
	}
	if int(index) > len(group.GetTxs()) {
		return "", types.ErrIndex
	}
	if index <= 0 {
		for i := range group.Txs {
			group.SignN(i, int32(SignType), key)
		}
		grouptx := group.Tx()
		txHex := types.Encode(grouptx)
		signedTx := hex.EncodeToString(txHex)
		return signedTx, nil
	} else {
		index -= 1
		group.SignN(int(index), int32(SignType), key)
		grouptx := group.Tx()
		txHex := types.Encode(grouptx)
		signedTx := hex.EncodeToString(txHex)
		return signedTx, nil
	}
	return "", nil
}

//output:
//type WalletAccounts struct {
//	Wallets []*WalletAccount
//type WalletAccount struct {
//	Acc   *Account
//	Label string
//获取钱包的地址列表
func (wallet *Wallet) ProcGetAccountList() (*types.WalletAccounts, error) {
	wallet.mtx.Lock()
	defer wallet.mtx.Unlock()

	//通过Account前缀查找获取钱包中的所有账户信息
	WalletAccStores, err := wallet.walletStore.GetAccountByPrefix("Account")
	if err != nil || len(WalletAccStores) == 0 {
		walletlog.Info("ProcGetAccountList", "GetAccountByPrefix:err", err)
		return nil, err
	}

	addrs := make([]string, len(WalletAccStores))
	for index, AccStore := range WalletAccStores {
		if len(AccStore.Addr) != 0 {
			addrs[index] = AccStore.Addr
		}
		//walletlog.Debug("ProcGetAccountList", "all AccStore", AccStore.String())
	}
	//获取所有地址对应的账户详细信息从account模块
	accounts, err := accountdb.LoadAccounts(wallet.api, addrs)
	if err != nil || len(accounts) == 0 {
		walletlog.Error("ProcGetAccountList", "LoadAccounts:err", err)
		return nil, err
	}

	//异常打印信息
	if len(WalletAccStores) != len(accounts) {
		walletlog.Error("ProcGetAccountList err!", "AccStores)", len(WalletAccStores), "accounts", len(accounts))
	}

	var WalletAccounts types.WalletAccounts
	WalletAccounts.Wallets = make([]*types.WalletAccount, len(WalletAccStores))

	for index, Account := range accounts {
		var WalletAccount types.WalletAccount
		//此账户还没有参与交易所在account模块没有记录
		if len(Account.Addr) == 0 {
			Account.Addr = addrs[index]
		}
		WalletAccount.Acc = Account
		WalletAccount.Label = WalletAccStores[index].GetLabel()
		WalletAccounts.Wallets[index] = &WalletAccount

		//walletlog.Info("ProcGetAccountList", "LoadAccounts:account", Account.String())
	}
	return &WalletAccounts, nil
}

//input:
//type ReqNewAccount struct {
//	Label string
//output:
//type WalletAccount struct {
//	Acc   *Account
//	Label string
//type Account struct {
//	Currency int32
//	Balance  int64
//	Frozen   int64
//	Addr     string
//创建一个新的账户
func (wallet *Wallet) ProcCreateNewAccount(Label *types.ReqNewAccount) (*types.WalletAccount, error) {
	wallet.mtx.Lock()
	defer wallet.mtx.Unlock()

	ok, err := wallet.CheckWalletStatus()
	if !ok {
		return nil, err
	}

	if Label == nil || len(Label.GetLabel()) == 0 {
		walletlog.Error("ProcCreateNewAccount Label is nil")
		return nil, types.ErrInputPara
	}

	//首先校验label是否已被使用
	WalletAccStores, err := wallet.walletStore.GetAccountByLabel(Label.GetLabel())
	if WalletAccStores != nil {
		walletlog.Error("ProcCreateNewAccount Label is exist in wallet!")
		return nil, types.ErrLabelHasUsed
	}

	var Account types.Account
	var walletAccount types.WalletAccount
	var WalletAccStore types.WalletAccountStore
	var cointype uint32
	if SignType == 1 {
		cointype = bipwallet.TypeBty
	} else if SignType == 2 {
		cointype = bipwallet.TypeYcc
	} else {
		cointype = bipwallet.TypeBty
	}

	//通过seed获取私钥, 首先通过钱包密码解锁seed然后通过seed生成私钥
	seed, err := wallet.getSeed(wallet.Password)
	if err != nil {
		walletlog.Error("ProcCreateNewAccount", "getSeed err", err)
		return nil, err
	}
	privkeyhex, err := GetPrivkeyBySeed(wallet.walletStore.db, seed)
	if err != nil {
		walletlog.Error("ProcCreateNewAccount", "GetPrivkeyBySeed err", err)
		return nil, err
	}
	privkeybyte, err := common.FromHex(privkeyhex)
	if err != nil || len(privkeybyte) == 0 {
		walletlog.Error("ProcCreateNewAccount", "FromHex err", err)
		return nil, err
	}

	pub, err := bipwallet.PrivkeyToPub(cointype, privkeybyte)
	if err != nil {
		seedlog.Error("ProcCreateNewAccount PrivkeyToPub", "err", err)
		return nil, types.ErrPrivkeyToPub
	}
	addr, err := bipwallet.PubToAddress(cointype, pub)
	if err != nil {
		seedlog.Error("ProcCreateNewAccount PubToAddress", "err", err)
		return nil, types.ErrPrivkeyToPub
	}

	Account.Addr = addr
	Account.Currency = 0
	Account.Balance = 0
	Account.Frozen = 0

	walletAccount.Acc = &Account
	walletAccount.Label = Label.GetLabel()

	//使用钱包的password对私钥加密 aes cbc
	Encrypted := CBCEncrypterPrivkey([]byte(wallet.Password), privkeybyte)
	WalletAccStore.Privkey = common.ToHex(Encrypted)
	WalletAccStore.Label = Label.GetLabel()
	WalletAccStore.Addr = addr

	//存储账户信息到wallet数据库中
	err = wallet.walletStore.SetWalletAccount(false, Account.Addr, &WalletAccStore)
	if err != nil {
		return nil, err
	}

	//获取地址对应的账户信息从account模块
	addrs := make([]string, 1)
	addrs[0] = addr
	accounts, err := accountdb.LoadAccounts(wallet.api, addrs)
	if err != nil {
		walletlog.Error("ProcCreateNewAccount", "LoadAccounts err", err)
		return nil, err
	}
	// 本账户是首次创建
	if len(accounts[0].Addr) == 0 {
		accounts[0].Addr = addr
	}
	walletAccount.Acc = accounts[0]

	//从blockchain模块同步Account.Addr对应的所有交易详细信息
	wallet.wg.Add(1)
	go wallet.ReqTxDetailByAddr(addr)

	return &walletAccount, nil
}

//input:
//type ReqWalletTransactionList struct {
//	FromTx []byte
//	Count  int32
//output:
//type WalletTxDetails struct {
//	TxDetails []*WalletTxDetail
//type WalletTxDetail struct {
//	Tx      *Transaction
//	Receipt *ReceiptData
//	Height  int64
//	Index   int64
//获取所有钱包的交易记录
func (wallet *Wallet) ProcWalletTxList(TxList *types.ReqWalletTransactionList) (*types.WalletTxDetails, error) {
	wallet.mtx.Lock()
	defer wallet.mtx.Unlock()

	if TxList == nil {
		walletlog.Error("ProcWalletTxList TxList is nil!")
		return nil, types.ErrInputPara
	}
	if TxList.GetDirection() != 0 && TxList.GetDirection() != 1 {
		walletlog.Error("ProcWalletTxList Direction err!")
		return nil, types.ErrInputPara
	}
	WalletTxDetails, err := wallet.walletStore.GetTxDetailByIter(TxList)
	if err != nil {
		walletlog.Error("ProcWalletTxList", "GetTxDetailByIter err", err)
		return nil, err
	}
	return WalletTxDetails, nil
}

//input:
//type ReqWalletImportPrivKey struct {
//	Privkey string
//	Label   string
//output:
//type WalletAccount struct {
//	Acc   *Account
//	Label string
//导入私钥，并且同时会导入交易
func (wallet *Wallet) ProcImportPrivKey(PrivKey *types.ReqWalletImportPrivKey) (*types.WalletAccount, error) {
	wallet.mtx.Lock()
	defer wallet.mtx.Unlock()

	ok, err := wallet.CheckWalletStatus()
	if !ok {
		return nil, err
	}

	if PrivKey == nil || len(PrivKey.GetLabel()) == 0 || len(PrivKey.GetPrivkey()) == 0 {
		walletlog.Error("ProcImportPrivKey input parameter is nil!")
		return nil, types.ErrInputPara
	}

	//校验label是否已经被使用
	Account, err := wallet.walletStore.GetAccountByLabel(PrivKey.GetLabel())
	if Account != nil {
		walletlog.Error("ProcImportPrivKey Label is exist in wallet!")
		return nil, types.ErrLabelHasUsed
	}

	var cointype uint32
	if SignType == 1 {
		cointype = bipwallet.TypeBty
	} else if SignType == 2 {
		cointype = bipwallet.TypeYcc
	} else {
		cointype = bipwallet.TypeBty
	}

	privkeybyte, err := common.FromHex(PrivKey.Privkey)
	if err != nil || len(privkeybyte) == 0 {
		walletlog.Error("ProcImportPrivKey", "FromHex err", err)
		return nil, types.ErrFromHex
	}

	pub, err := bipwallet.PrivkeyToPub(cointype, privkeybyte)
	if err != nil {
		seedlog.Error("ProcImportPrivKey PrivkeyToPub", "err", err)
		return nil, types.ErrPrivkeyToPub
	}
	addr, err := bipwallet.PubToAddress(cointype, pub)
	if err != nil {
		seedlog.Error("ProcImportPrivKey PrivkeyToPub", "err", err)
		return nil, types.ErrPrivkeyToPub
	}

	//对私钥加密
	Encryptered := CBCEncrypterPrivkey([]byte(wallet.Password), privkeybyte)
	Encrypteredstr := common.ToHex(Encryptered)
	//校验PrivKey对应的addr是否已经存在钱包中
	Account, err = wallet.walletStore.GetAccountByAddr(addr)
	if Account != nil {
		if Account.Privkey == Encrypteredstr {
			walletlog.Error("ProcImportPrivKey Privkey is exist in wallet!")
			return nil, types.ErrPrivkeyExist
		} else {
			walletlog.Error("ProcImportPrivKey!", "Account.Privkey", Account.Privkey, "input Privkey", PrivKey.Privkey)
			return nil, types.ErrPrivkey
		}
	}

	var walletaccount types.WalletAccount
	var WalletAccStore types.WalletAccountStore
	WalletAccStore.Privkey = Encrypteredstr //存储加密后的私钥
	WalletAccStore.Label = PrivKey.GetLabel()
	WalletAccStore.Addr = addr
	//存储Addr:label+privkey+addr到数据库
	err = wallet.walletStore.SetWalletAccount(false, addr, &WalletAccStore)
	if err != nil {
		walletlog.Error("ProcImportPrivKey", "SetWalletAccount err", err)
		return nil, err
	}

	//获取地址对应的账户信息从account模块
	addrs := make([]string, 1)
	addrs[0] = addr
	accounts, err := accountdb.LoadAccounts(wallet.api, addrs)
	if err != nil {
		walletlog.Error("ProcImportPrivKey", "LoadAccounts err", err)
		return nil, err
	}
	// 本账户是首次创建
	if len(accounts[0].Addr) == 0 {
		accounts[0].Addr = addr
	}
	walletaccount.Acc = accounts[0]
	walletaccount.Label = PrivKey.Label

	//从blockchain模块同步Account.Addr对应的所有交易详细信息
	wallet.wg.Add(1)
	go wallet.ReqTxDetailByAddr(addr)

	return &walletaccount, nil
}

//input:
//type ReqWalletSendToAddress struct {
//	From   string
//	To     string
//	Amount int64
//	Note   string
//output:
//type ReplyHash struct {
//	Hashe []byte
//发送一笔交易给对方地址，返回交易hash
func (wallet *Wallet) ProcSendToAddress(SendToAddress *types.ReqWalletSendToAddress) (*types.ReplyHash, error) {
	wallet.mtx.Lock()
	defer wallet.mtx.Unlock()

	if SendToAddress == nil {
		walletlog.Error("ProcSendToAddress input para is nil")
		return nil, types.ErrInputPara
	}
	if len(SendToAddress.From) == 0 || len(SendToAddress.To) == 0 {
		walletlog.Error("ProcSendToAddress input para From or To is nil!")
		return nil, types.ErrInputPara
	}

	ok, err := wallet.IsTransfer(SendToAddress.GetTo())
	if !ok {
		return nil, err
	}

	//获取from账户的余额从account模块，校验余额是否充足
	addrs := make([]string, 1)
	addrs[0] = SendToAddress.GetFrom()
	var accounts []*types.Account
	var tokenAccounts []*types.Account
	accounts, err = accountdb.LoadAccounts(wallet.api, addrs)
	if err != nil || len(accounts) == 0 {
		walletlog.Error("ProcSendToAddress", "LoadAccounts err", err)
		return nil, err
	}
	Balance := accounts[0].Balance
	amount := SendToAddress.GetAmount()
	if !SendToAddress.IsToken {
		if Balance < amount+wallet.FeeAmount {
			return nil, types.ErrInsufficientBalance
		}
	} else {
		//如果是token转账，一方面需要保证coin的余额满足fee，另一方面则需要保证token的余额满足转账操作
		if Balance < wallet.FeeAmount {
			return nil, types.ErrInsufficientBalance
		}

		if nil == accTokenMap[SendToAddress.TokenSymbol] {
			tokenAccDB, err := account.NewAccountDB("token", SendToAddress.TokenSymbol, nil)
			if err != nil {
				return nil, err
			}
			accTokenMap[SendToAddress.TokenSymbol] = tokenAccDB
		}
		tokenAccDB := accTokenMap[SendToAddress.TokenSymbol]
		tokenAccounts, err = tokenAccDB.LoadAccounts(wallet.api, addrs)
		if err != nil || len(tokenAccounts) == 0 {
			walletlog.Error("ProcSendToAddress", "Load Token Accounts err", err)
			return nil, err
		}
		tokenBalance := tokenAccounts[0].Balance
		if tokenBalance < amount {
			return nil, types.ErrInsufficientTokenBal
		}
	}
	addrto := SendToAddress.GetTo()
	note := SendToAddress.GetNote()
	priv, err := wallet.getPrivKeyByAddr(addrs[0])
	if err != nil {
		return nil, err
	}
	return wallet.sendToAddress(priv, addrto, amount, note, SendToAddress.IsToken, SendToAddress.TokenSymbol)
}

//type ReqWalletSetFee struct {
//	Amount int64
//设置钱包默认的手续费
func (wallet *Wallet) ProcWalletSetFee(WalletSetFee *types.ReqWalletSetFee) error {
	if WalletSetFee.Amount < minFee {
		walletlog.Error("ProcWalletSetFee err!", "Amount", WalletSetFee.Amount, "MinFee", minFee)
		return types.ErrInputPara
	}
	err := wallet.walletStore.SetFeeAmount(WalletSetFee.Amount)
	if err == nil {
		walletlog.Debug("ProcWalletSetFee success!")
		wallet.mtx.Lock()
		wallet.FeeAmount = WalletSetFee.Amount
		wallet.mtx.Unlock()
	}
	return err
}

//input:
//type ReqWalletSetLabel struct {
//	Addr  string
//	Label string
//output:
//type WalletAccount struct {
//	Acc   *Account
//	Label string
//设置某个账户的标签
func (wallet *Wallet) ProcWalletSetLabel(SetLabel *types.ReqWalletSetLabel) (*types.WalletAccount, error) {
	wallet.mtx.Lock()
	defer wallet.mtx.Unlock()

	if SetLabel == nil || len(SetLabel.Addr) == 0 || len(SetLabel.Label) == 0 {
		walletlog.Error("ProcWalletSetLabel input parameter is nil!")
		return nil, types.ErrInputPara
	}
	//校验label是否已经被使用
	Account, err := wallet.walletStore.GetAccountByLabel(SetLabel.GetLabel())
	if Account != nil {
		walletlog.Error("ProcWalletSetLabel Label is exist in wallet!")
		return nil, types.ErrLabelHasUsed
	}
	//获取地址对应的账户信息从钱包中,然后修改label
	Account, err = wallet.walletStore.GetAccountByAddr(SetLabel.Addr)
	if err == nil && Account != nil {
		oldLabel := Account.Label
		Account.Label = SetLabel.GetLabel()
		err := wallet.walletStore.SetWalletAccount(true, SetLabel.Addr, Account)
		if err == nil {
			//新的label设置成功之后需要删除旧的label在db的数据
			wallet.walletStore.DelAccountByLabel(oldLabel)

			//获取地址对应的账户详细信息从account模块
			addrs := make([]string, 1)
			addrs[0] = SetLabel.Addr
			accounts, err := accountdb.LoadAccounts(wallet.api, addrs)
			if err != nil || len(accounts) == 0 {
				walletlog.Error("ProcWalletSetLabel", "LoadAccounts err", err)
				return nil, err
			}
			var walletAccount types.WalletAccount
			walletAccount.Acc = accounts[0]
			walletAccount.Label = SetLabel.GetLabel()
			return &walletAccount, err
		}
	}
	return nil, err
}

//input:
//type ReqWalletMergeBalance struct {
//	To string
//output:
//type ReplyHashes struct {
//	Hashes [][]byte
//合并所有的balance 到一个地址
func (wallet *Wallet) ProcMergeBalance(MergeBalance *types.ReqWalletMergeBalance) (*types.ReplyHashes, error) {
	wallet.mtx.Lock()
	defer wallet.mtx.Unlock()

	ok, err := wallet.CheckWalletStatus()
	if !ok {
		return nil, err
	}

	if len(MergeBalance.GetTo()) == 0 {
		walletlog.Error("ProcMergeBalance input para is nil!")
		return nil, types.ErrInputPara
	}

	//获取钱包上的所有账户信息
	WalletAccStores, err := wallet.walletStore.GetAccountByPrefix("Account")
	if err != nil || len(WalletAccStores) == 0 {
		walletlog.Error("ProcMergeBalance", "GetAccountByPrefix err", err)
		return nil, err
	}

	addrs := make([]string, len(WalletAccStores))
	for index, AccStore := range WalletAccStores {
		if len(AccStore.Addr) != 0 {
			addrs[index] = AccStore.Addr
		}
	}
	//获取所有地址对应的账户信息从account模块
	accounts, err := accountdb.LoadAccounts(wallet.api, addrs)
	if err != nil || len(accounts) == 0 {
		walletlog.Error("ProcMergeBalance", "LoadAccounts err", err)
		return nil, err
	}

	//异常信息记录
	if len(WalletAccStores) != len(accounts) {
		walletlog.Error("ProcMergeBalance", "AccStores", len(WalletAccStores), "accounts", len(accounts))
	}
	//通过privkey生成一个pubkey然后换算成对应的addr
	cr, err := crypto.New(types.GetSignatureTypeName(SignType))
	if err != nil {
		walletlog.Error("ProcMergeBalance", "err", err)
		return nil, err
	}

	addrto := MergeBalance.GetTo()
	note := "MergeBalance"

	var ReplyHashes types.ReplyHashes

	for index, Account := range accounts {
		Privkey := WalletAccStores[index].Privkey
		//解密存储的私钥
		prikeybyte, err := common.FromHex(Privkey)
		if err != nil || len(prikeybyte) == 0 {
			walletlog.Error("ProcMergeBalance", "FromHex err", err, "index", index)
			continue
		}

		privkey := CBCDecrypterPrivkey([]byte(wallet.Password), prikeybyte)
		priv, err := cr.PrivKeyFromBytes(privkey)
		if err != nil {
			walletlog.Error("ProcMergeBalance", "PrivKeyFromBytes err", err, "index", index)
			continue
		}
		//过滤掉to地址
		if Account.Addr == addrto {
			continue
		}
		//获取账户的余额，过滤掉余额不足的地址
		amount := Account.GetBalance()
		if amount < wallet.FeeAmount {
			continue
		}
		amount = amount - wallet.FeeAmount
		v := &types.CoinsAction_Transfer{&types.CoinsTransfer{Amount: amount, Note: note}}
		transfer := &types.CoinsAction{Value: v, Ty: types.CoinsActionTransfer}
		//初始化随机数
		tx := &types.Transaction{Execer: []byte("coins"), Payload: types.Encode(transfer), Fee: wallet.FeeAmount, To: addrto, Nonce: wallet.random.Int63()}
		tx.SetExpire(time.Second * 120)
		tx.Sign(int32(SignType), priv)
		//walletlog.Info("ProcMergeBalance", "tx.Nonce", tx.Nonce, "tx", tx, "index", index)

		//发送交易信息给mempool模块
		msg := wallet.client.NewMessage("mempool", types.EventTx, tx)
		wallet.client.Send(msg, true)
		resp, err := wallet.client.Wait(msg)
		if err != nil {
			walletlog.Error("ProcMergeBalance", "Send tx err", err, "index", index)
			continue
		}
		//如果交易在mempool校验失败，不记录此交易
		reply := resp.GetData().(*types.Reply)
		if !reply.GetIsOk() {
			walletlog.Error("ProcMergeBalance", "Send tx err", string(reply.GetMsg()), "index", index)
			continue
		}
		ReplyHashes.Hashes = append(ReplyHashes.Hashes, tx.Hash())
	}
	return &ReplyHashes, nil
}

//input:
//type ReqWalletSetPasswd struct {
//	Oldpass string
//	Newpass string
//设置或者修改密码
func (wallet *Wallet) ProcWalletSetPasswd(Passwd *types.ReqWalletSetPasswd) error {
	wallet.mtx.Lock()
	defer wallet.mtx.Unlock()

	isok, err := wallet.CheckWalletStatus()
	if !isok && err == types.ErrSaveSeedFirst {
		return err
	}
	//保存钱包的锁状态，需要暂时的解锁，函数退出时再恢复回去
	tempislock := atomic.LoadInt32(&wallet.isWalletLocked)
	//wallet.isWalletLocked = false
	atomic.CompareAndSwapInt32(&wallet.isWalletLocked, 1, 0)

	defer func() {
		//wallet.isWalletLocked = tempislock
		atomic.CompareAndSwapInt32(&wallet.isWalletLocked, 0, tempislock)
	}()

	// 钱包已经加密需要验证oldpass的正确性
	if len(wallet.Password) == 0 && wallet.EncryptFlag == 1 {
		isok := wallet.walletStore.VerifyPasswordHash(Passwd.OldPass)
		if !isok {
			walletlog.Error("ProcWalletSetPasswd Verify Oldpasswd fail!")
			return types.ErrVerifyOldpasswdFail
		}
	}

	if len(wallet.Password) != 0 && Passwd.OldPass != wallet.Password {
		walletlog.Error("ProcWalletSetPasswd Oldpass err!")
		return types.ErrVerifyOldpasswdFail
	}

	//使用新的密码生成passwdhash用于下次密码的验证
	newBatch := wallet.walletStore.db.NewBatch(true)
	err = wallet.walletStore.SetPasswordHash(Passwd.NewPass, newBatch)
	if err != nil {
		walletlog.Error("ProcWalletSetPasswd", "SetPasswordHash err", err)
		return err
	}
	//设置钱包加密标志位
	err = wallet.walletStore.SetEncryptionFlag(newBatch)
	if err != nil {
		walletlog.Error("ProcWalletSetPasswd", "SetEncryptionFlag err", err)
		return err
	}
	//使用old密码解密seed然后用新的钱包密码重新加密seed
	seed, err := wallet.getSeed(Passwd.OldPass)
	if err != nil {
		walletlog.Error("ProcWalletSetPasswd", "getSeed err", err)
		return err
	}
	ok, err := SaveSeedInBatch(wallet.walletStore.db, seed, Passwd.NewPass, newBatch)
	if !ok {
		walletlog.Error("ProcWalletSetPasswd", "SaveSeed err", err)
		return err
	}

	//对所有存储的私钥重新使用新的密码加密,通过Account前缀查找获取钱包中的所有账户信息
	WalletAccStores, err := wallet.walletStore.GetAccountByPrefix("Account")
	if err != nil || len(WalletAccStores) == 0 {
		walletlog.Error("ProcWalletSetPasswd", "GetAccountByPrefix:err", err)
	}

	for _, AccStore := range WalletAccStores {
		//使用old Password解密存储的私钥
		storekey, err := common.FromHex(AccStore.GetPrivkey())
		if err != nil || len(storekey) == 0 {
			walletlog.Info("ProcWalletSetPasswd", "addr", AccStore.Addr, "FromHex err", err)
			continue
		}
		Decrypter := CBCDecrypterPrivkey([]byte(Passwd.OldPass), storekey)

		//使用新的密码重新加密私钥
		Encrypter := CBCEncrypterPrivkey([]byte(Passwd.NewPass), Decrypter)
		AccStore.Privkey = common.ToHex(Encrypter)
		err = wallet.walletStore.SetWalletAccountInBatch(true, AccStore.Addr, AccStore, newBatch)
		if err != nil {
			walletlog.Info("ProcWalletSetPasswd", "addr", AccStore.Addr, "SetWalletAccount err", err)
		}
	}

	newBatch.Write()
	wallet.Password = Passwd.NewPass
	return nil
}

//锁定钱包
func (wallet *Wallet) ProcWalletLock() error {
	//判断钱包是否已保存seed
	has, _ := HasSeed(wallet.walletStore.db)
	if !has {
		return types.ErrSaveSeedFirst
	}

	atomic.CompareAndSwapInt32(&wallet.isWalletLocked, 0, 1)
	atomic.CompareAndSwapInt32(&wallet.isTicketLocked, 0, 1)
	return nil
}

//input:
//type WalletUnLock struct {
//	Passwd  string
//	Timeout int64
//解锁钱包Timeout时间，超时后继续锁住
func (wallet *Wallet) ProcWalletUnLock(WalletUnLock *types.WalletUnLock) error {
	//判断钱包是否已保存seed
	has, _ := HasSeed(wallet.walletStore.db)
	if !has {
		return types.ErrSaveSeedFirst
	}
	// 钱包已经加密需要验证passwd的正确性
	if len(wallet.Password) == 0 && wallet.EncryptFlag == 1 {
		isok := wallet.walletStore.VerifyPasswordHash(WalletUnLock.Passwd)
		if !isok {
			walletlog.Error("ProcWalletUnLock Verify Oldpasswd fail!")
			return types.ErrVerifyOldpasswdFail
		}
	}

	//内存中已经记录password时的校验
	if len(wallet.Password) != 0 && WalletUnLock.Passwd != wallet.Password {
		return types.ErrInputPassword
	}
	//本钱包没有设置密码加密过,只需要解锁不需要记录解锁密码
	wallet.Password = WalletUnLock.Passwd

	//walletlog.Error("ProcWalletUnLock !", "WalletOrTicket", WalletUnLock.WalletOrTicket)

	//只解锁挖矿转账
	if WalletUnLock.WalletOrTicket {
		//wallet.isTicketLocked = false
		atomic.CompareAndSwapInt32(&wallet.isTicketLocked, 1, 0)
	} else {
		//wallet.isWalletLocked = false
		atomic.CompareAndSwapInt32(&wallet.isWalletLocked, 1, 0)
	}
	if WalletUnLock.Timeout != 0 {
		wallet.resetTimeout(WalletUnLock.WalletOrTicket, WalletUnLock.Timeout)
	}
	return nil

}

//解锁超时处理，需要区分整个钱包的解锁或者只挖矿的解锁
func (wallet *Wallet) resetTimeout(IsTicket bool, Timeout int64) {
	//只挖矿的解锁超时
	if IsTicket {
		if wallet.minertimeout == nil {
			wallet.minertimeout = time.AfterFunc(time.Second*time.Duration(Timeout), func() {
				//wallet.isTicketLocked = true
				atomic.CompareAndSwapInt32(&wallet.isTicketLocked, 0, 1)
			})
		} else {
			wallet.minertimeout.Reset(time.Second * time.Duration(Timeout))
		}
	} else { //整个钱包的解锁超时
		if wallet.timeout == nil {
			wallet.timeout = time.AfterFunc(time.Second*time.Duration(Timeout), func() {
				//wallet.isWalletLocked = true
				atomic.CompareAndSwapInt32(&wallet.isWalletLocked, 0, 1)
			})
		} else {
			wallet.timeout.Reset(time.Second * time.Duration(Timeout))
		}
	}
}

//wallet模块收到blockchain广播的addblock消息，需要解析钱包相关的tx并存储到db中
func (wallet *Wallet) ProcWalletAddBlock(block *types.BlockDetail) {
	if block == nil {
		walletlog.Error("ProcWalletAddBlock input para is nil!")
		return
	}
	//walletlog.Error("ProcWalletAddBlock", "height", block.GetBlock().GetHeight())
	txlen := len(block.Block.GetTxs())
	newbatch := wallet.walletStore.NewBatch(true)
	needflush := false
	for index := 0; index < txlen; index++ {
		blockheight := block.Block.Height*maxTxNumPerBlock + int64(index)
		heightstr := fmt.Sprintf("%018d", blockheight)

		var txdetail types.WalletTxDetail
		txdetail.Tx = block.Block.Txs[index]
		txdetail.Height = block.Block.Height
		txdetail.Index = int64(index)
		txdetail.Receipt = block.Receipts[index]
		txdetail.Blocktime = block.Block.BlockTime

		//获取Amount
		amount, err := txdetail.Tx.Amount()
		if err != nil {
			continue
		}
		txdetail.ActionName = txdetail.Tx.ActionName()
		txdetail.Amount = amount

		//获取from地址
		pubkey := block.Block.Txs[index].Signature.GetPubkey()
		addr := address.PubKeyToAddress(pubkey)
		txdetail.Fromaddr = addr.String()

		txdetailbyte, err := proto.Marshal(&txdetail)
		if err != nil {
			storelog.Error("ProcWalletAddBlock Marshal txdetail err", "Height", block.Block.Height, "index", index)
			continue
		}

		//from addr
		fromaddress := addr.String()
		if len(fromaddress) != 0 && wallet.AddrInWallet(fromaddress) {
			newbatch.Set(calcTxKey(heightstr), txdetailbyte)
			walletlog.Debug("ProcWalletAddBlock", "fromaddress", fromaddress, "heightstr", heightstr)
			continue
		}

		//toaddr
		toaddr := block.Block.Txs[index].GetTo()
		if len(toaddr) != 0 && wallet.AddrInWallet(toaddr) {
			newbatch.Set(calcTxKey(heightstr), txdetailbyte)
			walletlog.Debug("ProcWalletAddBlock", "toaddr", toaddr, "heightstr", heightstr)
		}

		if "ticket" == string(block.Block.Txs[index].Execer) {
			tx := block.Block.Txs[index]
			receipt := block.Receipts[index]
			if wallet.needFlushTicket(tx, receipt) {
				needflush = true
			}
		}
	}
	err := newbatch.Write()
	if err != nil {
		walletlog.Error("ProcWalletAddBlock newbatch.Write", "err", err)
		atomic.CompareAndSwapInt32(&wallet.fatalFailureFlag, 0, 1)
	}
	if needflush {
		//wallet.flushTicket()
	}
}

//wallet模块收到blockchain广播的delblock消息，需要解析钱包相关的tx并存db中删除
func (wallet *Wallet) ProcWalletDelBlock(block *types.BlockDetail) {
	if block == nil {
		walletlog.Error("ProcWalletDelBlock input para is nil!")
		return
	}
	//walletlog.Error("ProcWalletDelBlock", "height", block.GetBlock().GetHeight())

	txlen := len(block.Block.GetTxs())
	newbatch := wallet.walletStore.NewBatch(true)
	needflush := false
	for index := 0; index < txlen; index++ {
		blockheight := block.Block.Height*maxTxNumPerBlock + int64(index)
		heightstr := fmt.Sprintf("%018d", blockheight)
		if "ticket" == string(block.Block.Txs[index].Execer) {
			tx := block.Block.Txs[index]
			receipt := block.Receipts[index]
			if wallet.needFlushTicket(tx, receipt) {
				needflush = true
			}
		}
		//获取from地址
		addr := block.Block.Txs[index].From()
		fromaddress := addr
		if len(fromaddress) != 0 && wallet.AddrInWallet(fromaddress) {
			newbatch.Delete(calcTxKey(heightstr))
			//walletlog.Error("ProcWalletAddBlock", "fromaddress", fromaddress, "heightstr", heightstr)
			continue
		}
		//toaddr
		toaddr := block.Block.Txs[index].GetTo()
		if len(toaddr) != 0 && wallet.AddrInWallet(toaddr) {
			newbatch.Delete(calcTxKey(heightstr))
			//walletlog.Error("ProcWalletAddBlock", "toaddr", toaddr, "heightstr", heightstr)
		}
	}
	newbatch.Write()
	if needflush {
		wallet.flushTicket()
	}
}

func (wallet *Wallet) GetTxDetailByHashs(ReqHashes *types.ReqHashes) {
	//通过txhashs获取对应的txdetail
	msg := wallet.client.NewMessage("blockchain", types.EventGetTransactionByHash, ReqHashes)
	wallet.client.Send(msg, true)
	resp, err := wallet.client.Wait(msg)
	if err != nil {
		walletlog.Error("GetTxDetailByHashs EventGetTransactionByHash", "err", err)
		return
	}
	TxDetails := resp.GetData().(*types.TransactionDetails)
	if TxDetails == nil {
		walletlog.Info("GetTxDetailByHashs TransactionDetails is nil")
		return
	}

	//批量存储地址对应的所有交易的详细信息到wallet db中
	newbatch := wallet.walletStore.NewBatch(true)
	for _, txdetal := range TxDetails.Txs {
		height := txdetal.GetHeight()
		txindex := txdetal.GetIndex()

		blockheight := height*maxTxNumPerBlock + txindex
		heightstr := fmt.Sprintf("%018d", blockheight)
		var txdetail types.WalletTxDetail
		txdetail.Tx = txdetal.GetTx()
		txdetail.Height = txdetal.GetHeight()
		txdetail.Index = txdetal.GetIndex()
		txdetail.Receipt = txdetal.GetReceipt()
		txdetail.Blocktime = txdetal.GetBlocktime()
		txdetail.Amount = txdetal.GetAmount()
		txdetail.Fromaddr = txdetal.GetFromaddr()
		txdetail.ActionName = txdetal.GetTx().ActionName()

		//由于Withdraw的交易在blockchain模块已经做了from和to地址的swap的操作。
		//所以在此需要swap恢复回去。通过钱包的GetTxDetailByIter接口上送给前端时再做from和to地址的swap
		//确保保存在数据库中是的最原始的数据，提供给上层显示时可以做swap方便客户理解
		if txdetail.GetTx().IsWithdraw() {
			txdetail.Fromaddr, txdetail.Tx.To = txdetail.Tx.To, txdetail.Fromaddr
		}

		txdetailbyte, err := proto.Marshal(&txdetail)
		if err != nil {
			storelog.Error("GetTxDetailByHashs Marshal txdetail err", "Height", height, "index", txindex)
			return
		}
		newbatch.Set(calcTxKey(heightstr), txdetailbyte)
		//walletlog.Debug("GetTxDetailByHashs", "heightstr", heightstr, "txdetail", txdetail.String())
	}
	newbatch.Write()
}

//从blockchain模块同步addr参与的所有交易详细信息
func (wallet *Wallet) reqTxDetailByAddr(addr string) {
	if len(addr) == 0 {
		walletlog.Error("ReqTxInfosByAddr input addr is nil!")
		return
	}
	var txInfo types.ReplyTxInfo

	i := 0
	for {
		//首先从blockchain模块获取地址对应的所有交易hashs列表,从最新的交易开始获取
		var ReqAddr types.ReqAddr
		ReqAddr.Addr = addr
		ReqAddr.Flag = 0
		ReqAddr.Direction = 0
		ReqAddr.Count = int32(MaxTxHashsPerTime)
		if i == 0 {
			ReqAddr.Height = -1
			ReqAddr.Index = 0
		} else {
			ReqAddr.Height = txInfo.GetHeight()
			ReqAddr.Index = txInfo.GetIndex()
		}
		i++
		msg := wallet.client.NewMessage("blockchain", types.EventGetTransactionByAddr, &ReqAddr)
		wallet.client.Send(msg, true)
		resp, err := wallet.client.Wait(msg)
		if err != nil {
			walletlog.Error("ReqTxInfosByAddr EventGetTransactionByAddr", "err", err, "addr", addr)
			return
		}

		ReplyTxInfos := resp.GetData().(*types.ReplyTxInfos)
		if ReplyTxInfos == nil {
			walletlog.Info("ReqTxInfosByAddr ReplyTxInfos is nil")
			return
		}
		txcount := len(ReplyTxInfos.TxInfos)

		var ReqHashes types.ReqHashes
		ReqHashes.Hashes = make([][]byte, len(ReplyTxInfos.TxInfos))
		for index, ReplyTxInfo := range ReplyTxInfos.TxInfos {
			ReqHashes.Hashes[index] = ReplyTxInfo.GetHash()
			txInfo.Hash = ReplyTxInfo.GetHash()
			txInfo.Height = ReplyTxInfo.GetHeight()
			txInfo.Index = ReplyTxInfo.GetIndex()
		}
		wallet.GetTxDetailByHashs(&ReqHashes)
		if txcount < int(MaxTxHashsPerTime) {
			return
		}
	}
}

//生成一个随机的seed种子, 目前支持英文单词和简体中文
func (wallet *Wallet) genSeed(lang int32) (*types.ReplySeed, error) {
	seed, err := CreateSeed("", lang)
	if err != nil {
		walletlog.Error("genSeed", "CreateSeed err", err)
		return nil, err
	}
	var ReplySeed types.ReplySeed
	ReplySeed.Seed = seed
	return &ReplySeed, nil
}

//获取seed种子, 通过钱包密码
func (wallet *Wallet) getSeed(password string) (string, error) {
	ok, err := wallet.CheckWalletStatus()
	if !ok {
		return "", err
	}

	seed, err := GetSeed(wallet.walletStore.db, password)
	if err != nil {
		walletlog.Error("getSeed", "GetSeed err", err)
		return "", err
	}
	return seed, nil
}

//保存seed种子到数据库中, 并通过钱包密码加密, 钱包起来首先要设置seed
func (wallet *Wallet) saveSeed(password string, seed string) (bool, error) {

	//首先需要判断钱包是否已经设置seed，如果已经设置提示不需要再设置，一个钱包只能保存一个seed
	exit, err := HasSeed(wallet.walletStore.db)
	if exit {
		return false, types.ErrSeedExist
	}
	//入参数校验，seed必须是大于等于12个单词或者汉字
	if len(password) == 0 || len(seed) == 0 {
		return false, types.ErrInputPara
	}

	seedarry := strings.Fields(seed)
	curseedlen := len(seedarry)
	if curseedlen < SaveSeedLong {
		walletlog.Error("saveSeed VeriySeedwordnum", "curseedlen", curseedlen, "SaveSeedLong", SaveSeedLong)
		return false, types.ErrSeedWordNum
	}

	var newseed string
	for index, seedstr := range seedarry {
		if index != curseedlen-1 {
			newseed += seedstr + " "
		} else {
			newseed += seedstr
		}
	}

	//校验seed是否能生成钱包结构类型，从而来校验seed的正确性
	have, err := VerifySeed(newseed)
	if !have {
		walletlog.Error("saveSeed VerifySeed", "err", err)
		return false, types.ErrSeedWord
	}

	ok, err := SaveSeed(wallet.walletStore.db, newseed, password)
	//seed保存成功需要更新钱包密码
	if ok {
		var ReqWalletSetPasswd types.ReqWalletSetPasswd
		ReqWalletSetPasswd.OldPass = password
		ReqWalletSetPasswd.NewPass = password
		Err := wallet.ProcWalletSetPasswd(&ReqWalletSetPasswd)
		if Err != nil {
			walletlog.Error("saveSeed", "ProcWalletSetPasswd err", err)
		}
	}
	return ok, err
}

//获取地址对应的私钥
func (wallet *Wallet) ProcDumpPrivkey(addr string) (string, error) {
	wallet.mtx.Lock()
	defer wallet.mtx.Unlock()

	ok, err := wallet.CheckWalletStatus()
	if !ok {
		return "", err
	}
	if len(addr) == 0 {
		walletlog.Error("ProcDumpPrivkey input para is nil!")
		return "", types.ErrInputPara
	}

	priv, err := wallet.getPrivKeyByAddr(addr)
	if err != nil {
		return "", err
	}
	return common.ToHex(priv.Bytes()), nil
	//return strings.ToUpper(common.ToHex(priv.Bytes())), nil
}

//收到其他模块上报的系统有致命性故障，需要通知前端
func (wallet *Wallet) setFatalFailure(reportErrEvent *types.ReportErrEvent) {

	walletlog.Error("setFatalFailure", "reportErrEvent", reportErrEvent.String())
	if reportErrEvent.Error == "ErrDataBaseDamage" {
		atomic.StoreInt32(&wallet.fatalFailureFlag, 1)
	}
}

func (wallet *Wallet) getFatalFailure() int32 {
	return atomic.LoadInt32(&wallet.fatalFailureFlag)
}