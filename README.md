# nutmeg-notion-connect

NUTMEG の Notion を読み取り専用で読むCLI `nn` と、Claude Code 用のスキル（`SKILL.md`）。

## セットアップ

1. Notion のインテグレーションキー（NUTMEG ワークスペース用）を用意する
2. キーを置く
   ```sh
   mkdir -p ~/.config/nutmeg-notion
   echo 'NUTMEG_NOTION_KEY=ntn_...' > ~/.config/nutmeg-notion/env
   chmod 600 ~/.config/nutmeg-notion/env
   ```
3. コマンドとスキルとしてつなぐ
   ```sh
   ln -s "$PWD/nn.py" ~/.local/bin/nn
   ln -s "$PWD" ~/.claude/skills/nutmeg-notion-connect
   ```
4. `nn sync --full` で索引を作る（1分ちょっと）

Python 3.9 以上、標準ライブラリだけで動く。使い方は `nn -h` と `SKILL.md`。
