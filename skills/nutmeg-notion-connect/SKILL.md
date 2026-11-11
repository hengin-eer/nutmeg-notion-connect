---
name: nutmeg-notion-connect
description: NUTMEG（NUTFes技術局）の Notion ワークスペースを、CLI `nn` で読み取り専用で読む。手元の索引とキャッシュで、APIを叩く回数と出力のトークンを抑える。NUTMEG の Notion の内容（議事録・企画書・各プロジェクトのページやタスク・メンバーDBなど）が必要なとき、notion.so/nutfes-nutmeg の URL が出てきたとき、Notion のページを確認・比較・レビューしたいときに使う。非公開ページは WebFetch では読めないので、こちらを使う。
allowed-tools: Bash(nn *)
---

# nutmeg-notion-connect

`nn` は NUTMEG の Notion を読む CLI。`<id>` には Notion の URL・ID・別名を渡せる。

- `nn` が無い（`command -v nn` が失敗する）：ユーザーに https://github.com/hengin-eer/nutmeg-notion-connect#インストール を案内する
- キーが無いと言われた：ユーザーに「自分のターミナルで `nn auth login`」を頼む。キーを会話に貼ってもらわない

## 基本の流れ（トークンを節約する順）

0. どこを見ればいいか分からない：`nn overview` … ワークスペースの全体像（中身の多いDB、よく出る言葉＝プロジェクト名や会議の種類、代の接頭辞、最近動いている場所）。手元の索引から集計する。`--db <id>` でDBの中だけ
1. 探す：`nn find FinanSu 議事録` … 手元の索引からタイトル検索。`--since 2026-10-01` `--db <id>` `--type db` で絞れる
2. 形をつかむ：`nn outline <id>` … 見出しと各セクションの行数だけ
3. 必要なところだけ読む：`nn read <id> -s 見出しの一部`
4. 全文は最後の手段：`nn read <id>`（12000字を超えると見出し一覧だけ返る。`--all` で全部）

## その他のコマンド

- `nn grep <id> '正規表現'` … 当たった行と、どの見出しの中か（`-` で始まる式は `nn grep <id> -- '-x'`）
- `nn diff <id>` … 前回読んだ版との差分。「直ったか確認して」はまずこれ
- `nn children <id>` … 子ページ・子DBの一覧
- `nn dbs 単語` … DBの一覧。同じ名前のDBが複数あることが多いので、更新日で選ぶ
- `nn db <id> --schema` … 列と選択肢
- `nn db <id> --title X --where '列~文字' --where '列=値' --since 2026-10-01 --props 列1,列2 --limit 10` … 行をTSVで
- `nn read <id> --links` … リンクのURLも出す（ふだんは文字だけ）
- `nn alias 名前 <id>` … よく使うDBやページに別名をつける（`nn alias` で一覧）

## キャッシュ

- `read` などは更新日時を1回だけ確認し、変わっていなければキャッシュを返す。入れ子の多いページは初回だけ数十秒かかる
- `--cached` で更新確認もしない（API0回）。`--fresh` で取り直す
- 索引は `overview` `find` `dbs` の前に、10分以上古ければ差分同期する（数秒）。更新できなければ古い索引で続ける。新しく作られたページが見つからないときは `nn sync`、DBが増えたときは `nn sync --full`（1分ほど）

## 注意

- 読み取り専用。Notion を直してほしいと言われたら、ユーザーがそのまま貼れる形で修正文を出す
- `nn` を同時に複数走らせない（APIの回数制限があるため）
- ユーザー情報は読めないので、人の列はIDの頭8文字で出る
- キーを出力やファイルに書き出さない
