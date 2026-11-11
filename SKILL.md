---
name: nutmeg-notion-connect
description: NUTMEG（NUTFes技術局）のNotionワークスペースを読み取り専用で読むためのCLI `nn`。議事録・企画書・ハッカソン資料・キックオフ資料・メンバーDBなど NUTMEG の Notion の内容が必要なとき、notion.so/nutfes-nutmeg のURLが出てきたとき、Notion のページを確認・レビュー・比較したいときに使う。WebFetch では非公開ページが読めないので、こちらを使う。
---

# nutmeg-notion-connect

`nn` は NUTMEG の Notion を読むためのCLI（PATHに入っている。無ければ `python3 ~/.claude/skills/nutmeg-notion-connect/nn.py`）。
IDの代わりに NotionのURL・ハイフンなしID・別名を渡してよい。別名は `nn alias` で一覧、`nn alias 名前 <IDかURL>` で登録（`~/.config/nutmeg-notion/aliases.json` に保存。よく使うDBにつけておく）。

## 基本の流れ（トークンを節約する順）

1. 探す：`nn find 46th 企画書` … 手元の索引からタイトル検索（API0回）。`--since 2026-10-01` `--db <別名>` `--type db` で絞れる
2. 形をつかむ：`nn outline <id>` … 見出しと各セクションの行数だけ
3. 必要なところだけ読む：`nn read <id> -s アイスブレイク` … 見出しに含まれる文字で指定
4. 全文は最後の手段：`nn read <id>`（12000字を超えると見出し一覧だけ返る。`--all` で全部）

その他
- `nn grep <id> 'TODO|【】'` … 当たった行と、どの見出しの中か
- `nn diff <id>` … 前回読んだ版との差分。「直ったか確認して」はまずこれ
- `nn children <id>` … 子ページ・子DBの一覧
- `nn dbs 週刊` … DBの一覧。`nn db <id> --schema` で列、`nn db <id> --title 46th --where 'project=Bingo' --props Date --limit 10` で行をTSVに
- `nn read <id> --links` … リンクのURLも出す（ふだんは文字だけ）

## キャッシュ

- ページは `~/.cache/nutmeg-notion/pages/` にmarkdownで保存。`read` などは更新日時を1回だけ確認して、変わってなければキャッシュを返す
- 入れ子の多いページは初回だけ数十秒かかる。同じページを何度も取り直さない
- `--cached` で更新確認もしない（API0回）。`--fresh` で強制的に取り直す
- 索引は `find` のときに10分おきに自動で差分同期（検索API＋別名つきDB＋ページ数の多いDB）。新しく作られたページが見つからないときは `nn sync`、DBが増えたときは `nn sync --full`（1分ちょっと）

## 注意

- 読み取り専用。書き込みは403になる。Notionを直してほしいと言われたら、ユーザーがそのまま貼れる形で修正文を出す
- APIは並列で叩かない（`nn` を同時に複数走らせない）。429はCLI側で待って再試行する
- ユーザー情報は読めないので、人の列はIDの頭8文字で出る
- ワークスペースの構成やタイトルの付け方の癖は `references/workspace.md` を見る
- キーは環境変数 `NUTMEG_NOTION_KEY` か `~/.config/nutmeg-notion/env`。キーを出力やファイルに書き出さない
