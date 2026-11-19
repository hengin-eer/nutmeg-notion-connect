# nutmeg-notion-connect

NUTMEG（NUTFes技術局）の Notion を読み取り専用で読む CLI `nn` と、Claude Code 用のスキル。

エージェントに Notion を読ませるときのトークンを減らすのが目的。
- 生の JSON ではなく、短い markdown で返す
- タイトルの索引を手元に持ち、探すときに API を叩かない
- 本文は Markdown API v2 で一括取得し、装飾や長い画像 URL を省いた短い形にする
- ページは更新されていなければキャッシュを返す
- 見出し単位・行範囲で必要なところだけ読む

## インストール

### 1. `nn` を入れる

[Releases](https://github.com/hengin-eer/nutmeg-notion-connect/releases) から自分の OS 向けのファイルを落として、PATH の通った場所に置く。

```sh
# 例：Linux (x86_64)
curl -L https://github.com/hengin-eer/nutmeg-notion-connect/releases/latest/download/nn_linux_amd64.tar.gz | tar xz nn
mv nn ~/.local/bin/
```

Go が入っていれば、こちらでもよい。

```sh
go install github.com/hengin-eer/nutmeg-notion-connect/cmd/nn@latest
```

### 2. キーを登録する

NUTMEG ワークスペース用の Notion インテグレーションキーを用意して、自分のターミナルで実行する。

```sh
nn auth login     # キーを貼り付ける（画面には出ない）。保存前に使えるか確かめる
nn sync --full    # 索引を作り、最近のページを先読みする（1〜2分）
```

- キーは `~/.config/nutmeg-notion-connect/credentials`（本人だけ読める）に保存される
- 環境変数 `NUTMEG_NOTION_KEY` があれば、そちらが優先される
- `nn auth status` で確認、`nn auth logout` で削除

### 3. スキルを入れる（Claude Code）

```
/plugin marketplace add hengin-eer/nutmeg-notion-connect
/plugin install nutmeg-notion-connect@nutmeg-notion-connect
```

## 使い方

`nn -h` と [SKILL.md](skills/nutmeg-notion-connect/SKILL.md) を参照。まずは `nn overview` でワークスペースの全体像を見るとよい。

## 保存場所

| もの | 場所 |
| --- | --- |
| キー・別名 | `~/.config/nutmeg-notion-connect/` |
| 索引・ページのキャッシュ | `~/.cache/nutmeg-notion-connect/` |

`XDG_CONFIG_HOME` / `XDG_CACHE_HOME` があればそちらを使う。

ページ本文のキャッシュは、リンク URL を含む圧縮済み Markdown を1つ保存する。
`--links` の有無でキャッシュを共有し、通常の出力では URL を除く。署名付きのメディア URL は保存しない。前の版は `diff` 用に別途保存する。
旧形式のキャッシュは `--cached` でも取り直す。旧 `.links.md` は使わない。

初回・更新時は、更新日時の確認1回と本文取得1回が基本。本文が省略された場合は不足ブロックを順に取得し、元の位置へ差し込む。権限がなく取得できない部分は `[見られないブロック]` と表示する。
Notion-Version は `2022-06-28` のまま、本文に `markdown_version=v2` を指定する。

## 開発

```sh
go vet ./...
go test ./...
go build -o bin/nn ./cmd/nn
```

| 場所 | 中身 |
| --- | --- |
| `cmd/nn` | 入口 |
| `internal/cli` | コマンド |
| `internal/notion` | API の呼び出し（回数制限・再試行） |
| `internal/render` | Markdown v2 の取得・省略部分の補完・短い本文への変換 |
| `internal/pagecache` | ページのキャッシュと前の版 |
| `internal/index` | タイトルの索引・同期・overview |
| `internal/config` | キー・別名・保存場所 |

変換層の golden テストは `internal/render/testdata/` の合成データを使う。
手元の調査データと字数を比較する場合は、`NN_SURVEY_DIR` に `*.v2.md` / `*.nn.txt` のディレクトリを指定して `go test ./internal/render -run TestLocalSurvey -v` を実行する。未指定・データなしならスキップする。比較結果は数字だけを出す。

[Markdown API の仕様](https://developers.notion.com/reference/retrieve-page-markdown)と[省略・権限の扱い](https://developers.notion.com/guides/data-apis/working-with-markdown-content#unknown-blocks-truncation-and-permissions)を参照。未知の拡張タグは本文を落とさずそのまま残す。省略部分の循環や挿入位置が不明な応答は、未完成の本文をキャッシュせずエラーにする。

リリースは `v*` のタグを push すると、GitHub Actions と GoReleaser が OS 別のファイルを Releases に置く。

## ライセンス

MIT
