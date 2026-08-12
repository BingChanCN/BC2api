// 猜硬币游戏侧车 —— 演示主站游戏账本的完整下注/结算流。
//
// 运行（与主服务同一 Docker 网络）：
//
//	SUB2API_BASE_URL=http://sub2api:8080 \
//	PLUGIN_SECRET_FILE=/run/secrets/plugin-api \
//	go run ./sidecar
//
// 行为：
//   - GET /            渲染游戏页（通过主站 postMessage 桥调用本侧车）
//   - POST /play       下注并立即结算：赢 → payout(2x)，输 → 不返还
//   - POST /balance    查询用户余额（管理接口，示例用途）
//
// 安全要点（与 docs/PLUGINS.md 一致）：
//   - 只信任主服务注入的 X-Sub2API-User-ID，不信任请求体里的 user_id
//   - round_id 由侧车生成，绝不信任客户端
//   - 账本调用走主站内部 API，重复请求由主站幂等保证
package main

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"
)

var (
	baseURL    = envOr("SUB2API_BASE_URL", "http://sub2api:8080")
	secretFile = envOr("PLUGIN_SECRET_FILE", ".api-secret")
	gameID     = "coinflip"
	maxStake   = 10.0 // 与 manifest.game.max_stake 保持一致
)

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

type ledgerRequest struct {
	GameID  string  `json:"game_id"`
	Kind    string  `json:"kind"`
	RoundID string  `json:"round_id"`
	UserID  int64   `json:"user_id"`
	Amount  float64 `json:"amount"`
	Note    string  `json:"note"`
}

type ledgerResponse struct {
	Code    int                 `json:"code"`
	Message string              `json:"message"`
	Data    *ledgerResponseData `json:"data,omitempty"`
	Reason  string              `json:"reason,omitempty"`
}

type ledgerResponseData struct {
	Replayed     bool    `json:"replayed"`
	BalanceAfter float64 `json:"balance_after"`
}

func main() {
	secret, err := os.ReadFile(secretFile)
	if err != nil {
		log.Fatalf("read secret file: %v", err)
	}
	secret = bytes.TrimSpace(secret)

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, indexHTML)
	})
	mux.HandleFunc("/play", func(w http.ResponseWriter, r *http.Request) {
		handlePlay(w, r, secret)
	})

	log.Printf("coinflip sidecar listening on :8080 (ledger=%s)", baseURL)
	if err := http.ListenAndServe(":8080", mux); err != nil {
		log.Fatal(err)
	}
}

func handlePlay(w http.ResponseWriter, r *http.Request, secret []byte) {
	userID, err := strconv.ParseInt(r.Header.Get("X-Sub2API-User-ID"), 10, 64)
	if err != nil || userID <= 0 {
		http.Error(w, "invalid user identity", http.StatusUnauthorized)
		return
	}

	var body struct {
		Amount float64 `json:"amount"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&body); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	if body.Amount <= 0 || body.Amount > maxStake {
		http.Error(w, fmt.Sprintf("amount must be in (0, %.2f]", maxStake), http.StatusBadRequest)
		return
	}

	roundID := newRoundID()
	client := &http.Client{Timeout: 10 * time.Second}

	// 1. 先扣下注
	if _, err := callLedger(client, secret, ledgerRequest{
		GameID: gameID, Kind: "stake", RoundID: roundID, UserID: userID, Amount: body.Amount, Note: "下注",
	}); err != nil {
		writeJSON(w, http.StatusPaymentRequired, map[string]any{"ok": false, "error": err.Error()})
		return
	}

	// 2. 立即开奖
	won := flip()
	note := "猜错"
	result := "lose"
	if won {
		// 3. 赢家发奖（stake 已扣，payout 是奖金）
		if _, err := callLedger(client, secret, ledgerRequest{
			GameID: gameID, Kind: "payout", RoundID: roundID, UserID: userID, Amount: body.Amount * 2, Note: "猜中",
		}); err != nil {
			// 发奖失败时退还下注，保证玩家不亏
			_, _ = callLedger(client, secret, ledgerRequest{
				GameID: gameID, Kind: "refund", RoundID: roundID, UserID: userID, Amount: body.Amount, Note: "发奖失败退款",
			})
			writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "payout failed, stake refunded"})
			return
		}
		note = "猜中"
		result = "win"
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "round_id": roundID, "result": result, "note": note,
	})
}

// callLedger 调用主站内部游戏账本；失败时返回可读错误（含主站 reason）。
func callLedger(client *http.Client, secret []byte, req ledgerRequest) (*ledgerResponseData, error) {
	payload, _ := json.Marshal(req)
	httpReq, err := http.NewRequest(http.MethodPost, baseURL+"/api/v1/internal/game/transactions", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+string(secret))

	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))

	var out ledgerResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("ledger http %d: %s", resp.StatusCode, string(raw))
	}
	if out.Code != 0 && resp.StatusCode != http.StatusOK {
		reason := out.Reason
		if reason == "" {
			reason = out.Message
		}
		return nil, fmt.Errorf("ledger rejected: %s", reason)
	}
	return out.Data, nil
}

func newRoundID() string {
	buf := make([]byte, 12)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("round-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(buf)
}

func flip() bool {
	buf := make([]byte, 1)
	_, _ = rand.Read(buf)
	return buf[0]&1 == 0
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

const indexHTML = `<!doctype html>
<html lang="zh">
<head>
<meta charset="utf-8"><title>猜硬币</title>
<style>
body{font-family:system-ui,sans-serif;background:#111827;color:#f9fafb;display:flex;flex-direction:column;align-items:center;justify-content:center;min-height:100vh;margin:0}
button{background:#10b981;color:#fff;border:0;border-radius:8px;padding:10px 24px;font-size:16px;cursor:pointer}
input{padding:8px;border-radius:8px;border:1px solid #374151;background:#1f2937;color:#fff;width:120px}
#status{margin-top:16px;font-size:14px;color:#9ca3af}
</style>
</head>
<body>
<h2>猜硬币</h2>
<p>下注金额（站点余额）</p>
<div><input id="amount" type="number" min="0.1" step="0.1" value="1"> <button onclick="play()">下注并开奖</button></div>
<div id="status"></div>
<script>
const params = new URLSearchParams(location.hash.slice(1))
const capability = params.get('capability') || ''
function invoke(request) {
  const request_id = crypto.randomUUID()
  return new Promise((resolve, reject) => {
    function onMessage(event) {
      const data = event.data
      if (!data || data.protocol !== 'sub2api-plugin-v1' || data.type !== 'result') return
      if (data.capability !== capability || data.request_id !== request_id) return
      window.removeEventListener('message', onMessage)
      if (data.ok) resolve(data.response)
      else reject(data.error)
    }
    window.addEventListener('message', onMessage)
    parent.postMessage({protocol:'sub2api-plugin-v1', type:'invoke', capability, request_id, request}, '*')
  })
}
async function play() {
  const status = document.getElementById('status')
  const amount = Number(document.getElementById('amount').value)
  status.textContent = '结算中…'
  try {
    const res = await invoke({method:'POST', path:'play', content_type:'application/json', body:JSON.stringify({amount})})
    status.textContent = '结果: ' + (res.result === 'win' ? '🎉 猜中，奖金已到账' : '😢 猜错，下注已扣除') + '（'+res.note+'）'
  } catch (err) {
    status.textContent = '失败: ' + (err && (err.detail || err.message) ? (err.detail || err.message) : '请检查余额是否足够')
  }
}
</script>
</body></html>
`
