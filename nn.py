#!/usr/bin/env python3
"""nn: NUTMEG Notion を読むための小さなCLI（読み取り専用・標準ライブラリのみ）。

使い方は `nn -h` か SKILL.md を参照。
"""
import argparse
import datetime as dt
import difflib
import json
import os
import re
import sys
import time
import urllib.error
import urllib.request
from pathlib import Path

HERE = Path(__file__).resolve().parent
CONFIG = Path(os.environ.get("XDG_CONFIG_HOME", Path.home() / ".config")) / "nutmeg-notion" / "env"
CACHE = Path(os.environ.get("XDG_CACHE_HOME", Path.home() / ".cache")) / "nutmeg-notion"
PAGES = CACHE / "pages"
INDEX = CACHE / "index.json"
API = "https://api.notion.com/v1/"
NOTION_VERSION = "2022-06-28"
JST = dt.timezone(dt.timedelta(hours=9))
DEFAULT_MAX = 12000
MIN_INTERVAL = 0.35  # Notion の上限は平均3req/s
SYNC_TTL = 600  # find の前に自動で差分同期する間隔（秒）

_last_call = 0.0
_key = None


class NotionError(Exception):
    pass


# ---------- 設定・API ----------

def load_key():
    global _key
    if _key:
        return _key
    k = os.environ.get("NUTMEG_NOTION_KEY")
    if not k and CONFIG.exists():
        for line in CONFIG.read_text().splitlines():
            name, _, val = line.partition("=")
            if name.strip() in ("NUTMEG_NOTION_KEY", "NOTION_INTEGRATION_KEY"):
                k = val.strip().strip('"').strip("'")
    if not k:
        sys.exit(f"キーが見つからへん。環境変数 NUTMEG_NOTION_KEY か {CONFIG} に NUTMEG_NOTION_KEY=... を書いてな")
    _key = k
    return k


def api(path, body=None, method=None):
    global _last_call
    data = json.dumps(body).encode() if body is not None else None
    method = method or ("POST" if body is not None else "GET")
    for attempt in range(8):
        wait = MIN_INTERVAL - (time.time() - _last_call)
        if wait > 0:
            time.sleep(wait)
        _last_call = time.time()
        req = urllib.request.Request(API + path, data=data, method=method, headers={
            "Authorization": "Bearer " + load_key(),
            "Notion-Version": NOTION_VERSION,
            "Content-Type": "application/json",
        })
        try:
            with urllib.request.urlopen(req, timeout=60) as r:
                return json.load(r)
        except urllib.error.HTTPError as e:
            if e.code == 429 or e.code >= 500:
                time.sleep(float(e.headers.get("Retry-After") or 2 ** attempt))
                continue
            try:
                msg = json.load(e).get("message", "")
            except Exception:
                msg = ""
            hint = {
                401: "キーが無効",
                403: "権限なし（このインテグレーションは読み取り専用で、ユーザー情報も読めない）",
                404: "見つからへん。IDが違うか、インテグレーションに共有されてへん",
            }.get(e.code, "")
            raise NotionError(f"HTTP {e.code} {hint} {msg}".strip())
        except urllib.error.URLError as e:
            if attempt < 3:
                time.sleep(2)
                continue
            raise NotionError(f"接続エラー: {e}")
    raise NotionError("リトライ上限に達した（429/5xxが続いた）")


def paginate(path, body=None):
    cur = None
    while True:
        if body is None:
            sep = "&" if "?" in path else "?"
            r = api(path + sep + "page_size=100" + (f"&start_cursor={cur}" if cur else ""))
        else:
            b = dict(body)
            b.setdefault("page_size", 100)
            if cur:
                b["start_cursor"] = cur
            r = api(path, b)
        yield from r["results"]
        if not r.get("has_more"):
            return
        cur = r["next_cursor"]


# ---------- ID・別名 ----------

def aliases():
    p = HERE / "aliases.json"
    return json.loads(p.read_text()) if p.exists() else {}


def norm_id(s):
    """URL・ハイフンなし・別名を受け付けて、ハイフン付きUUIDにする。"""
    a = aliases()
    if s in a:
        s = a[s]
    m = re.findall(r"[0-9a-fA-F]{32}|[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}", s.split("?")[0])
    if not m:
        sys.exit(f"IDとして読めへん: {s}")
    h = m[-1].replace("-", "").lower()
    return f"{h[:8]}-{h[8:12]}-{h[12:16]}-{h[16:20]}-{h[20:]}"


def alias_of(i):
    for k, v in aliases().items():
        if norm_id(v) == i:
            return k
    return None


# ---------- 文字列化 ----------

def jst(iso):
    if not iso:
        return ""
    t = dt.datetime.fromisoformat(iso.replace("Z", "+00:00"))
    return t.astimezone(JST).strftime("%Y-%m-%d %H:%M")


def rt(arr, links=False):
    out = []
    for x in arr or []:
        s = x.get("plain_text", "")
        if links and x.get("href") and x.get("type") != "mention":
            s += f"（{x['href']}）"
        out.append(s)
    return "".join(out)


def obj_title(o):
    if o.get("object") == "database":
        return rt(o.get("title")) or "(無題DB)"
    for v in o.get("properties", {}).values():
        if v.get("type") == "title":
            return rt(v["title"]) or "(無題)"
    return "(無題)"


def prop_str(v):
    t = v.get("type")
    x = v.get(t)
    if x is None:
        return ""
    if t in ("title", "rich_text"):
        return rt(x)
    if t in ("select", "status"):
        return x.get("name", "")
    if t == "multi_select":
        return ",".join(o["name"] for o in x)
    if t == "people":
        return ",".join(p.get("name") or p["id"][:8] for p in x)
    if t == "date":
        return (x.get("start") or "") + (("→" + x["end"]) if x.get("end") else "")
    if t in ("created_time", "last_edited_time"):
        return jst(x)
    if t == "checkbox":
        return "✓" if x else ""
    if t == "relation":
        return f"{len(x)}件"
    if t == "formula":
        return str(x.get(x.get("type")) or "")
    if t == "rollup":
        if x.get("type") == "array":
            return ",".join(prop_str(i) for i in x["array"])
        return str(x.get(x.get("type")) or "")
    if t == "unique_id":
        return f"{x.get('prefix') or ''}{x.get('number')}"
    if t in ("created_by", "last_edited_by"):
        return x.get("name") or ""
    if t == "files":
        return ",".join(f.get("name", "") for f in x)
    return str(x)


# ---------- ページ → markdown ----------

def block_children(bid):
    return list(paginate(f"blocks/{bid}/children"))


def render(bid, depth=0, links=False):
    lines = []
    num = 0
    for b in block_children(bid):
        t = b["type"]
        v = b.get(t) or {}
        ind = "  " * depth
        text = rt(v.get("rich_text"), links) if isinstance(v, dict) else ""
        kids_depth = depth + 1
        line = None
        num = num + 1 if t == "numbered_list_item" else 0

        if t == "paragraph":
            line = text
        elif t in ("heading_1", "heading_2", "heading_3"):
            line = "#" * int(t[-1]) + " " + text
            kids_depth = depth  # 見出しトグルの中身は字下げしない
        elif t == "bulleted_list_item":
            line = "- " + text
        elif t == "numbered_list_item":
            line = f"{num}. " + text
        elif t == "to_do":
            line = ("- [x] " if v.get("checked") else "- [ ] ") + text
        elif t == "toggle":
            line = "▸ " + text
        elif t == "quote":
            line = "> " + text
        elif t == "callout":
            icon = (v.get("icon") or {}).get("emoji", "")
            line = "> " + (icon + " " if icon else "") + text
        elif t == "code":
            lines.append(f"{ind}```{v.get('language', '')}")
            lines += [ind + l for l in text.splitlines()]
            lines.append(ind + "```")
            continue
        elif t == "equation":
            line = "$" + v.get("expression", "") + "$"
        elif t == "divider":
            line = "---"
        elif t == "child_page":
            lines.append(f"{ind}📄 {v.get('title')} ({b['id']})")
            continue
        elif t == "child_database":
            lines.append(f"{ind}🗃 {v.get('title')} ({b['id']})")
            continue
        elif t == "link_to_page":
            lines.append(f"{ind}→ {v.get(v.get('type'))}")
            continue
        elif t in ("bookmark", "embed", "link_preview"):
            cap = rt(v.get("caption"))
            line = f"[{t}] {v.get('url', '')}" + (f" {cap}" if cap else "")
        elif t in ("image", "file", "pdf", "video", "audio"):
            cap = rt(v.get("caption")) or v.get("name", "")
            url = (v.get("external") or {}).get("url") or (v.get("file") or {}).get("url", "")
            line = f"[{t}{': ' + cap if cap else ''}]" + (f" {url}" if links and url else "")
        elif t == "table":
            rows = [r["table_row"]["cells"] for r in block_children(b["id"]) if r["type"] == "table_row"]
            for i, cells in enumerate(rows):
                lines.append(ind + "| " + " | ".join(rt(c, links).replace("|", "\\|").replace("\n", " ") for c in cells) + " |")
                if i == 0:
                    lines.append(ind + "|" + "---|" * len(cells))
            continue
        elif t in ("column_list", "column"):
            lines += render(b["id"], depth, links)
            continue
        elif t == "synced_block":
            src = (v.get("synced_from") or {}).get("block_id") or b["id"]
            lines += render(src, depth, links)
            continue
        elif t in ("table_of_contents", "breadcrumb"):
            continue
        else:
            line = f"[{t}]"

        if line is not None:
            for j, l in enumerate(line.split("\n")):
                lines.append(ind + (l if j == 0 else "  " + l))
        if b.get("has_children"):
            lines += render(b["id"], kids_depth, links)
    return lines


def tidy(lines):
    out = []
    for l in lines:
        l = l.rstrip()
        if not l and (not out or not out[-1]):
            continue
        out.append(l)
    while out and not out[-1]:
        out.pop()
    return "\n".join(out) + "\n"


def get_meta(pid):
    try:
        return api(f"pages/{pid}")
    except NotionError as e:
        if "404" in str(e) or "400" in str(e):
            try:
                d = api(f"databases/{pid}")
            except NotionError:
                raise e
            sys.exit(f"これはDBやで（{obj_title(d)}）。`nn db {pid}` で見てな")
        raise


def load_page(pid, links=False, fresh=False, cached_only=False):
    """markdown・メタ情報を返す。last_edited_time が変わってなければキャッシュを使う。"""
    PAGES.mkdir(parents=True, exist_ok=True)
    suffix = ".links" if links else ""
    md_p = PAGES / f"{pid}{suffix}.md"
    meta_p = PAGES / f"{pid}{suffix}.json"
    old = json.loads(meta_p.read_text()) if meta_p.exists() else None
    if cached_only and old and md_p.exists():
        return md_p.read_text(), old
    m = get_meta(pid)
    edited = m["last_edited_time"]
    now = time.time()
    if old and md_p.exists() and not fresh and old["edited"] == edited:
        # last_edited_time は分単位なので、取得が編集直後なら取り直す
        edited_ts = dt.datetime.fromisoformat(edited.replace("Z", "+00:00")).timestamp()
        if old["fetched"] - edited_ts > 90:
            return md_p.read_text(), old
    body = tidy(render(pid, links=links))
    meta = {"id": pid, "title": obj_title(m), "edited": edited, "fetched": now, "url": m.get("url")}
    if md_p.exists() and md_p.read_text() != body:
        (PAGES / f"{pid}{suffix}.prev.md").write_text(md_p.read_text())
        meta["prev_edited"] = old["edited"] if old else None
    elif old and old.get("prev_edited"):
        meta["prev_edited"] = old["prev_edited"]
    md_p.write_text(body)
    meta_p.write_text(json.dumps(meta, ensure_ascii=False))
    return body, meta


def header(meta, body):
    return f"{meta['title']}  [{meta['id']} / 更新 {jst(meta['edited'])} / {len(body)}字]"


def headings(body):
    res = []
    for i, l in enumerate(body.splitlines(), 1):
        m = re.match(r"(#{1,3}) (.*)", l)
        if m:
            res.append((i, len(m.group(1)), m.group(2)))
    return res


# ---------- 索引（全ページのタイトル一覧） ----------

def load_index():
    if INDEX.exists():
        return json.loads(INDEX.read_text())
    return {"synced_at": None, "items": {}}


def sync(full=False, quiet=False):
    idx = {"synced_at": None, "items": {}} if full else load_index()
    since = idx["synced_at"]
    stop_before = None
    if since:
        stop_before = dt.datetime.fromisoformat(since) - dt.timedelta(minutes=10)
    started = dt.datetime.now(dt.timezone.utc)
    n = 0

    def add(o):
        nonlocal n
        par = o.get("parent", {})
        idx["items"][o["id"]] = {
            "t": obj_title(o),
            "e": o["last_edited_time"],
            "c": o.get("created_time", ""),
            "p": par.get(par.get("type")) if par.get("type") != "workspace" else None,
            "o": "db" if o["object"] == "database" else "page",
            "x": o.get("archived") or o.get("in_trash") or False,
        }
        n += 1
        if not quiet and n % 500 == 0:
            print(f"  ...{n}件", file=sys.stderr)

    # 1) 検索API（ワークスペース全体。ただし大きいDBの中身は取りこぼす）
    body = {"sort": {"direction": "descending", "timestamp": "last_edited_time"}}
    for o in paginate("search", body):
        edited = dt.datetime.fromisoformat(o["last_edited_time"].replace("Z", "+00:00"))
        if stop_before and edited < stop_before:
            break
        add(o)
    # 2) DBを直接読む。全体同期なら全DB、差分なら aliases.json のDBだけ
    if stop_before:
        dbs = {norm_id(v) for v in aliases().values()}
    else:
        dbs = {i for i, it in idx["items"].items() if it["o"] == "db" and not it.get("x")}
    q = {}
    if stop_before:
        q = {"filter": {"timestamp": "last_edited_time", "last_edited_time": {"on_or_after": stop_before.isoformat()}}}
    for did in dbs:
        try:
            for o in paginate(f"databases/{did}/query", q):
                add(o)
        except NotionError:
            pass  # リンクドDBなど、直接読めないものは飛ばす
    idx["synced_at"] = started.isoformat()
    CACHE.mkdir(parents=True, exist_ok=True)
    INDEX.write_text(json.dumps(idx, ensure_ascii=False))
    if not quiet:
        print(f"同期した: {n}件取得 / 合計{len(idx['items'])}件", file=sys.stderr)
    return idx


def ensure_index():
    idx = load_index()
    if idx["synced_at"]:
        age = (dt.datetime.now(dt.timezone.utc) - dt.datetime.fromisoformat(idx["synced_at"])).total_seconds()
        if age < SYNC_TTL:
            return idx
        return sync(quiet=True)
    print("初回の索引づくり中（数十秒かかる）...", file=sys.stderr)
    return sync()


def parent_label(idx, pid):
    if not pid:
        return ""
    a = alias_of(pid)
    if a:
        return a
    p = idx["items"].get(pid)
    if p and p["o"] == "db":
        return p["t"][:20]
    return ""


# ---------- コマンド ----------

def cmd_find(a):
    idx = ensure_index()
    words = [w.lower() for w in a.words]
    in_db = norm_id(a.db) if a.db else None
    hits = []
    for i, it in idx["items"].items():
        if it.get("x"):
            continue
        t = it["t"].lower()
        if not all(w in t for w in words):
            continue
        if a.type and it["o"] != a.type:
            continue
        if in_db and it["p"] != in_db:
            continue
        if a.since and it["e"][:10] < a.since:
            continue
        hits.append((i, it))
    hits.sort(key=lambda x: x[1]["e"], reverse=True)
    for i, it in hits[: a.limit]:
        pl = parent_label(idx, it["p"])
        mark = "🗃 " if it["o"] == "db" else ""
        print(f"{i}  {jst(it['e'])[:10]}  {mark}{it['t']}" + (f"  [{pl}]" if pl else ""))
    if len(hits) > a.limit:
        print(f"…ほか{len(hits) - a.limit}件（--limit で増やせる）")
    if not hits:
        print("見つからへん。言葉を減らすか、`nn sync --full` で索引を作り直してみて")


def cmd_outline(a):
    pid = norm_id(a.id)
    body, meta = load_page(pid, cached_only=a.cached)
    print(header(meta, body))
    hs = headings(body)
    total = len(body.splitlines())
    for k, (ln, lv, txt) in enumerate(hs):
        end = next((h[0] for h in hs[k + 1:] if h[1] <= lv), total + 1)
        print(f"{'  ' * (lv - 1)}{'#' * lv} {txt}  (L{ln}, {end - ln}行)")
    if not hs:
        print(f"(見出しなし、{total}行)")


def section(body, query):
    lines = body.splitlines()
    hs = headings(body)
    q = query.lower()
    for k, (ln, lv, txt) in enumerate(hs):
        if q in txt.lower():
            end = next((h[0] for h in hs[k + 1:] if h[1] <= lv), len(lines) + 1)
            return "\n".join(lines[ln - 1:end - 1]) + "\n"
    return None


def cmd_read(a):
    pid = norm_id(a.id)
    body, meta = load_page(pid, links=a.links, fresh=a.fresh, cached_only=a.cached)
    out = body
    if a.section:
        out = section(body, a.section)
        if out is None:
            print(f"見出し「{a.section}」が見つからへん。見出し一覧:")
            for _, lv, txt in headings(body):
                print("  " * (lv - 1) + txt)
            return
    if a.lines:
        s, _, e = a.lines.partition("-")
        ls = out.splitlines()
        out = "\n".join(ls[int(s) - 1:int(e) if e else len(ls)]) + "\n"
    print(header(meta, body))
    if len(out) > a.max and not a.all:
        print(f"（{len(out)}字あるので全文は出さへん。--section で見出しを指定するか、--all で全部出す）")
        for _, lv, txt in headings(out):
            print("  " * (lv - 1) + "#" * lv + " " + txt)
        return
    sys.stdout.write(out)


def cmd_grep(a):
    pid = norm_id(a.id)
    body, meta = load_page(pid, cached_only=a.cached)
    print(header(meta, body))
    rx = re.compile(a.pattern, re.I)
    path = {}
    n = 0
    for i, l in enumerate(body.splitlines(), 1):
        m = re.match(r"(#{1,3}) (.*)", l)
        if m:
            lv = len(m.group(1))
            path = {k: v for k, v in path.items() if k < lv}
            path[lv] = m.group(2)
        if rx.search(l):
            ctx = " > ".join(path[k] for k in sorted(path))
            print(f"L{i} [{ctx}] {l.strip()}")
            n += 1
    if not n:
        print("当たりなし")


def cmd_diff(a):
    pid = norm_id(a.id)
    body, meta = load_page(pid)
    prev = PAGES / f"{pid}.prev.md"
    print(header(meta, body))
    if not prev.exists():
        print("前の版がまだない（読むのが初めて）。次に変更があったら差分が出せる")
        return
    old = prev.read_text().splitlines()
    d = list(difflib.unified_diff(old, body.splitlines(), "前の版 " + jst(meta.get("prev_edited")), "今 " + jst(meta["edited"]), lineterm="", n=a.context))
    print("\n".join(d) if d else "差分なし")


def cmd_children(a):
    pid = norm_id(a.id)
    body, meta = load_page(pid, cached_only=a.cached)
    print(header(meta, body))
    n = 0
    for l in body.splitlines():
        if l.lstrip().startswith(("📄", "🗃", "→")):
            print(l.strip())
            n += 1
    if not n:
        print("子ページなし")


def cmd_dbs(a):
    idx = ensure_index()
    words = [w.lower() for w in a.words]
    for i, it in sorted(idx["items"].items(), key=lambda x: x[1]["e"], reverse=True):
        if it["o"] == "db" and not it.get("x") and all(w in it["t"].lower() for w in words):
            al = alias_of(i)
            print(f"{i}  {jst(it['e'])[:10]}  {it['t']}" + (f"  (別名: {al})" if al else ""))


def cmd_db(a):
    did = norm_id(a.id)
    d = api(f"databases/{did}")
    props = d["properties"]
    tname = next(k for k, v in props.items() if v["type"] == "title")
    if a.schema:
        print(f"{obj_title(d)}  [{did}]")
        for k, v in props.items():
            x = v.get(v["type"])
            opts = [o["name"] for o in x.get("options", [])] if isinstance(x, dict) else []
            print(f"- {k} ({v['type']})" + (f": {', '.join(opts[:20])}" if opts else ""))
        return
    filters = []
    if a.title:
        filters.append({"property": tname, "title": {"contains": a.title}})
    if a.since:
        filters.append({"timestamp": "last_edited_time", "last_edited_time": {"on_or_after": a.since}})
    for w in a.where or []:
        m = re.match(r"(.+?)(~|=)(.*)", w)
        if not m or m.group(1) not in props:
            sys.exit(f"--where は 列名~文字列（含む）か 列名=値（一致）。列名: {', '.join(props)}")
        k, op, val = m.groups()
        t = props[k]["type"]
        if t in ("title", "rich_text", "url", "email", "phone_number"):
            filters.append({"property": k, t: {"contains" if op == "~" else "equals": val}})
        elif t in ("select", "status"):
            filters.append({"property": k, t: {"equals": val}})
        elif t in ("multi_select", "relation", "people"):
            filters.append({"property": k, t: {"contains": val}})
        elif t == "checkbox":
            filters.append({"property": k, t: {"equals": val.lower() in ("1", "true", "yes", "✓")}})
        elif t == "number":
            filters.append({"property": k, t: {"equals": float(val)}})
        elif t == "date":
            filters.append({"property": k, t: {"on_or_after": val}})
        else:
            sys.exit(f"{k}（{t}）では絞り込めへん。--filter-json を使ってな")
    if a.filter_json:
        filters.append(json.loads(a.filter_json))
    body = {"sorts": [{"timestamp": "last_edited_time", "direction": "descending"}], "page_size": min(a.limit, 100)}
    if filters:
        body["filter"] = filters[0] if len(filters) == 1 else {"and": filters}
    cols = [c.strip() for c in a.props.split(",")] if a.props else []
    for c in cols:
        if c not in props:
            sys.exit(f"列「{c}」がない。列名: {', '.join(props)}")
    print("\t".join(["id", "更新", tname] + cols))
    n = 0
    for p in paginate(f"databases/{did}/query", body):
        row = [p["id"], jst(p["last_edited_time"])[:10], obj_title(p)]
        row += [prop_str(p["properties"][c]).replace("\t", " ").replace("\n", " ") for c in cols]
        print("\t".join(row))
        n += 1
        if n >= a.limit:
            print(f"（{a.limit}件で打ち切り。--limit で増やせる）")
            break


def cmd_sync(a):
    sync(full=a.full)


def main():
    ap = argparse.ArgumentParser(prog="nn", description="NUTMEG Notion 読み取りCLI。IDの代わりにNotionのURLや別名（aliases.json）も使える")
    sp = ap.add_subparsers(dest="cmd", required=True)

    p = sp.add_parser("find", help="タイトルで探す（手元の索引を使う）")
    p.add_argument("words", nargs="+")
    p.add_argument("--db", help="このDBの中だけ（IDか別名）")
    p.add_argument("--type", choices=["page", "db"])
    p.add_argument("--since", help="この日付（YYYY-MM-DD）以降に更新されたもの")
    p.add_argument("--limit", type=int, default=20)
    p.set_defaults(f=cmd_find)

    def page_cmd(name, fn, help_):
        p = sp.add_parser(name, help=help_)
        p.add_argument("id")
        p.add_argument("--cached", action="store_true", help="更新確認もせずキャッシュを使う（API0回）")
        p.set_defaults(f=fn)
        return p

    page_cmd("outline", cmd_outline, "見出しだけ（各セクションの行数つき）")
    p = page_cmd("read", cmd_read, "ページをmarkdownで読む")
    p.add_argument("--section", "-s", help="この文字を含む見出しのセクションだけ")
    p.add_argument("--lines", help="行の範囲（例: 20-60）")
    p.add_argument("--links", action="store_true", help="リンクのURLも出す")
    p.add_argument("--fresh", action="store_true", help="キャッシュを使わず取り直す")
    p.add_argument("--max", type=int, default=DEFAULT_MAX, help="これより長いと見出し一覧だけ返す")
    p.add_argument("--all", action="store_true", help="長くても全部出す")
    p = page_cmd("grep", cmd_grep, "ページ内を正規表現で探す（どの見出しの中か付き）")
    p.add_argument("pattern")
    page_cmd("children", cmd_children, "子ページ・DB・リンクの一覧")
    p = sp.add_parser("diff", help="前回読んだ版との差分")
    p.add_argument("id")
    p.add_argument("--context", "-C", type=int, default=1)
    p.set_defaults(f=cmd_diff)

    p = sp.add_parser("dbs", help="DBの一覧（言葉で絞れる）")
    p.add_argument("words", nargs="*")
    p.set_defaults(f=cmd_dbs)

    p = sp.add_parser("db", help="DBの行をTSVで")
    p.add_argument("id")
    p.add_argument("--schema", action="store_true", help="列の一覧と選択肢")
    p.add_argument("--title", help="タイトルに含む")
    p.add_argument("--where", action="append", help="列名~文字列 / 列名=値（何回でも）")
    p.add_argument("--since", help="この日付以降に更新")
    p.add_argument("--props", help="出す列（カンマ区切り）")
    p.add_argument("--filter-json", help="Notion APIのfilterをそのまま")
    p.add_argument("--limit", type=int, default=30)
    p.set_defaults(f=cmd_db)

    p = sp.add_parser("sync", help="索引を差分で更新")
    p.add_argument("--full", action="store_true", help="作り直す")
    p.set_defaults(f=cmd_sync)

    a = ap.parse_args()
    try:
        a.f(a)
    except NotionError as e:
        sys.exit(f"エラー: {e}")
    except BrokenPipeError:
        pass


if __name__ == "__main__":
    main()
