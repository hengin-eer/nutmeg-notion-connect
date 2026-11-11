# Claude Code 計測ハーネス

標準ライブラリだけを使う開発用コマンド。配布対象の `cmd/nn` とは別に実行する。

```sh
go build -o bin/nn ./cmd/nn
cp bench/tasks.example.json bench/tasks.local.json
# tasks.local.json の prompt を実際の読み取り依頼に編集する
# NUTMEG_NOTION_KEY または ~/.config/nutmeg-notion-connect/credentials を利用
# 既定は Claude のログイン（OAuth）を利用。事前にログインしておく。
go run ./bench run --cond raw --model haiku --task find-notes --trials 1 --max-budget-usd 0.5
go run ./bench run --cond nn --model haiku --task find-notes --trials 1 --max-budget-usd 0.5 --nn-bin ./bin/nn
go run ./bench report
```

**実行は課金される。** 上限は1試行ごと。タスク数×試行数×条件数だけ実行する。
上限は Claude CLI に渡す値であり、最後の呼び出しで超過する可能性がある。
動作確認は Haiku 1タスク、各条件1回だけに留める。
`--timeout 10m` で試行の時間制限、`--out` / report の `--in` で結果を選べる。
`--task` を省略するとタスク全部を実行する。`--label nn-v2 --nn-bin /path/to/nn` で改善版を比較できる。

raw はキーを NOTION_KEY に渡し、直接 API を使うよう system prompt に追記する。
nn は同じキーを NUTMEG_NOTION_KEY にも渡し、スキル本文を system prompt に明示的に追記する。
自動スキル探索は無効にしており、スキルの選択性能ではなく、スキルを与えた効果を測る。

既定はログイン利用モード（結果の `isolation: "login"`）。HOME と CLAUDE_CONFIG_DIR は
実環境のままで、既存の Claude ログイン（OAuth）を使う。
`--setting-sources '' --strict-mcp-config --mcp-config '{"mcpServers":{}}'`
でユーザー・プロジェクト設定と MCP を隔離し、`--disable-slash-commands` でスキル探索を止める。
各試行は新しい一時 cwd を使うので、cwd 単位の自動メモリは空から始まる。
`~/.claude/CLAUDE.md` は両条件に同じく乗る一定の上乗せになる。
ログイン利用時の `total_cost_usd` は API 料金換算の参考値であり、実際の請求額ではない。

`--bare` 指定時だけ完全隔離モード（`isolation: "bare"`）となり、
一時 HOME / CLAUDE_CONFIG_DIR と Claude CLI の `--bare` を使う。
ANTHROPIC_API_KEY が必須で、未設定なら試行前にエラーにする。
両モードとも nn 用の XDG_CONFIG_HOME / XDG_CACHE_HOME は一時ディレクトリにする。
Bash だけを `--tools` / `--allowedTools` で有効にし、`dontAsk` で対話承認を防ぐ。
PATH は専用ディレクトリに一般的なツールへのリンクを置いて構成し、nn 条件だけ nn を加える。
これは再現性のための隔離で、OS のセキュリティサンドボックスではない。
管理ポリシーや CLI バージョンの差は別途記録して比較すること。

`--nn-cache warm|cold` は既定 warm。nn 条件では実際の `~/.cache/nutmeg-notion-connect`
（XDG_CACHE_HOME があればそちら）と `~/.config/nutmeg-notion-connect/aliases.json`
（XDG_CONFIG_HOME があればそちら）を各試行の一時 XDG ディレクトリにコピーする。
credentials はコピーせず、元のキャッシュや別名は変更しない。コピー元がなければ空から始まる。
シンボリックリンクなど通常ファイル・ディレクトリ以外のコピー元はエラーにする。
cold は空の状態から始めるため、索引作成などの初期コストも含む。
raw 条件ではコピーしない。選択した設定を `nn_cache` に記録する。
report は isolation / nn_cache が異なる試行を別グループとして集計し、回答表にも表示する。

比較対象のバイナリにも NN_STATS_FILE 対応が必要。未対応版の0回は正しい計測にならない。
NN_STATS_FILE には HTTP 送信試行ごとに固定文字列を追記する（リトライとネットワーク失敗も含む）。
raw の API 回数は Bash のコマンド文字列内の api.notion.com 出現数の**推定値**。
ループ、スクリプト、変数化、失敗で実際のリクエスト回数とずれる。
ツール回数は stream-json の tool_use ID を重複排除して数える。
トークン、費用、時間、ターンは最終 result の値。wall_ms は子プロセスの実測時間。
result が無い・不正なストリーム・終了失敗・予算到達はエラーとして保存する。
集計は失敗試行も含む平均で、エラー数も併記する。回答を並べて人が正しさを確認する。
expect は大文字小文字を区別する部分文字列の一致数であり、自動採点ではない。

タスクは JSON 配列（id、prompt、任意の expect 配列）。local タスクと results/ は gitignore 済み。
ページ ID・URL・個人名を公開対象ファイルに入れない。読み取り専用の Notion 統合を使い、書き込みを依頼しない。
JSONL は1試行1行で追記する。キーそのものと secret_/ntn_ トークンを保存前に伏せる。
ストリーム・stderr は保存しない。結果には回答由来の非公開情報が残り得るので公開しない。
一時ディレクトリは終了時に削除する。Notion キーは環境変数のみで子に渡す。
