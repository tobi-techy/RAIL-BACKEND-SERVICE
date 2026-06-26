package blend

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
)

func TestEncodeMultiSendData_Packing(t *testing.T) {
	steps := []ActionStep{
		{To: "0x0000000000000000000000000000000000000001", Data: "0xabcd", Value: "0"},                   // call
		{To: "0x0000000000000000000000000000000000000002", Data: "0x", Value: "0", IsDelegateCall: true}, // delegatecall, empty data
	}
	out, err := encodeMultiSendData(steps)
	if err != nil {
		t.Fatalf("encodeMultiSendData: %v", err)
	}

	// selector
	if got := hex.EncodeToString(out[:4]); got != selectorMultiSend {
		t.Fatalf("selector = %s, want %s", got, selectorMultiSend)
	}
	// offset word == 32
	if got := bigIntView(out[4:36]); got.uint64() != 32 {
		t.Fatalf("bytes offset = %d, want 32", got.uint64())
	}
	// length word
	length := bigIntView(out[36:68]).uint64()

	// expected packed length: step1 (1+20+32+32+2) + step2 (1+20+32+32+0) = 87 + 85 = 172
	if length != 172 {
		t.Fatalf("packed length = %d, want 172", length)
	}
	packed := out[68 : 68+length]

	// step 1: op byte 0 (call)
	if packed[0] != 0x00 {
		t.Fatalf("step1 op = %d, want 0 (call)", packed[0])
	}
	if got := hex.EncodeToString(packed[1:21]); got != "0000000000000000000000000000000000000001" {
		t.Fatalf("step1 to = %s", got)
	}
	// dataLen word for step1 == 2, then data abcd
	dataLen1 := bigIntView(packed[53:85]).uint64()
	if dataLen1 != 2 {
		t.Fatalf("step1 dataLen = %d, want 2", dataLen1)
	}
	if got := hex.EncodeToString(packed[85:87]); got != "abcd" {
		t.Fatalf("step1 data = %s, want abcd", got)
	}

	// step 2 begins at offset 87: op byte 1 (delegatecall)
	if packed[87] != 0x01 {
		t.Fatalf("step2 op = %d, want 1 (delegatecall)", packed[87])
	}

	// total calldata length: 4 + 32 + 32 + rightPad32(172)=192 = 260
	if len(out) != 4+32+32+192 {
		t.Fatalf("total calldata len = %d, want %d", len(out), 4+32+32+192)
	}
}

func TestPreValidatedSignature(t *testing.T) {
	owner := "0x28f05fef1234567890abcdef1234567890250ead" // arbitrary 20-byte addr
	sig, err := preValidatedSignature(owner)
	if err != nil {
		t.Fatal(err)
	}
	if len(sig) != 65 {
		t.Fatalf("sig len = %d, want 65", len(sig))
	}
	// r: first 12 bytes zero, then the 20-byte owner
	if !bytes.Equal(sig[:12], make([]byte, 12)) {
		t.Fatalf("r high 12 bytes not zero")
	}
	if got := hex.EncodeToString(sig[12:32]); got != strings.TrimPrefix(strings.ToLower(owner), "0x") {
		t.Fatalf("r owner = %s, want %s", got, owner)
	}
	// s == 0
	if !bytes.Equal(sig[32:64], make([]byte, 32)) {
		t.Fatalf("s not zero")
	}
	// v == 1
	if sig[64] != 0x01 {
		t.Fatalf("v = %d, want 1", sig[64])
	}
}

func TestEncodeSafeExecTransaction_Structure(t *testing.T) {
	owner := "0x1111111111111111111111111111111111111111"
	ms, err := encodeMultiSendData([]ActionStep{{To: "0x0000000000000000000000000000000000000003", Data: "0x1234", Value: "0"}})
	if err != nil {
		t.Fatal(err)
	}
	out, err := encodeSafeExecTransaction(ms, owner)
	if err != nil {
		t.Fatal(err)
	}
	if got := hex.EncodeToString(out[:4]); got != selectorExecTransaction {
		t.Fatalf("selector = %s, want %s", got, selectorExecTransaction)
	}
	body := out[4:]
	// word0: to = MultiSend (low 20 bytes)
	if got := "0x" + hex.EncodeToString(body[12:32]); !strings.EqualFold(got, multiSendAddress) {
		t.Fatalf("to = %s, want %s", got, multiSendAddress)
	}
	// word3: operation == 1 (delegatecall)
	if op := bigIntView(body[3*32 : 4*32]).uint64(); op != 1 {
		t.Fatalf("operation = %d, want 1", op)
	}
	// word2: data offset == 320 (10 head words * 32)
	if off := bigIntView(body[2*32 : 3*32]).uint64(); off != 320 {
		t.Fatalf("data offset = %d, want 320", off)
	}
	// signatures offset (word9) must point past the data tail.
	sigOff := bigIntView(body[9*32 : 10*32]).uint64()
	wantSigOff := uint64(320 + 32 + len(rightPad32(ms)))
	if sigOff != wantSigOff {
		t.Fatalf("sig offset = %d, want %d", sigOff, wantSigOff)
	}
	// signatures length == 65, last byte v==1
	sigLen := bigIntView(body[sigOff : sigOff+32]).uint64()
	if sigLen != 65 {
		t.Fatalf("sig len = %d, want 65", sigLen)
	}
	if body[sigOff+32+64] != 0x01 {
		t.Fatalf("sig v byte != 1")
	}
}

func TestParseActionPlan_Withdraw(t *testing.T) {
	raw := json.RawMessage(`{
		"safeAddress":"0xSafe",
		"destinationChainId":8453,
		"payloads":[{
			"chainId":8453,
			"vaultAddress":"0xVault",
			"steps":[
				{"kind":"forceDeallocate","to":"0xaa","data":"0x01"},
				{"kind":"liquidityReset","to":"0xbb","data":"0x02","delegateCall":true},
				{"kind":"withdraw","to":"0xcc","data":"0x03"}
			]
		}]
	}`)
	plan, err := ParseActionPlan(raw)
	if err != nil {
		t.Fatalf("parse withdraw: %v", err)
	}
	if plan.DeployType != deployMultisend {
		t.Fatalf("deployType = %q, want multisend", plan.DeployType)
	}
	if len(plan.Steps) != 3 {
		t.Fatalf("steps = %d, want 3", len(plan.Steps))
	}
	if plan.Steps[0].IsDelegateCall {
		t.Fatalf("forceDeallocate must not be delegatecall")
	}
	if !plan.Steps[1].IsDelegateCall {
		t.Fatalf("liquidityReset must be delegatecall")
	}
	if plan.Steps[2].IsDelegateCall {
		t.Fatalf("withdraw must not be delegatecall")
	}
	for i, s := range plan.Steps {
		if s.ChainID != 8453 {
			t.Fatalf("step %d chainId = %d, want 8453", i, s.ChainID)
		}
	}
}

func TestParseActionPlan_Deposit(t *testing.T) {
	raw := json.RawMessage(`{
		"originChainId":8453,
		"steps":[
			{"kind":"approve","to":"0xToken","data":"0x01","value":"0","chainId":8453},
			{"kind":"deposit","to":"0xRouter","data":"0x02","value":"0","chainId":8453}
		]
	}`)
	plan, err := ParseActionPlan(raw)
	if err != nil {
		t.Fatalf("parse deposit: %v", err)
	}
	if plan.DeployType != deployDirect {
		t.Fatalf("deployType = %q, want direct", plan.DeployType)
	}
	if len(plan.Steps) != 2 || plan.Steps[0].Kind != "approve" || plan.Steps[1].Kind != "deposit" {
		t.Fatalf("unexpected steps: %+v", plan.Steps)
	}
}

func TestParseActionPlan_Unrecognized(t *testing.T) {
	if _, err := ParseActionPlan(json.RawMessage(`{"foo":"bar"}`)); err == nil {
		t.Fatal("expected error for unrecognized payload")
	}
}

// bigIntView reads a 32-byte big-endian word as a uint64 (low 8 bytes), for assertions.
type bigIntView []byte

func (b bigIntView) uint64() uint64 {
	var n uint64
	for _, x := range b {
		n = n<<8 | uint64(x)
	}
	return n
}
