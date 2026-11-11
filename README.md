# nutmeg-notion-connect

NUTMEG（NUTFes技術局）の Notion を読み取り専用で読む CLI `nn` と、Claude Code 用のスキル。

エージェントに Notion を読ませるときのトークンを減らすのが目的。
- 生の JSON ではなく、短い markdown で返す
- タイトルの索引を手元に持ち、探すときに API を叩かない
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
nn sync --full    # 索引を作る（1分ほど）
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

## 開発

```sh
go test ./...
go build -o bin/nn ./cmd/nn
```

| 場所 | 中身 |
| --- | --- |
| `cmd/nn` | 入口 |
| `internal/cli` | コマンド |
| `internal/notion` | API の呼び出し（回数制限・再試行） |
| `internal/render` | ブロック → markdown |
| `internal/pagecache` | ページのキャッシュと前の版 |
| `internal/index` | タイトルの索引・同期・overview |
| `internal/config` | キー・別名・保存場所 |

リリースは `v*` のタグを push すると、GitHub Actions と GoReleaser が OS 別のファイルを Releases に置く。
