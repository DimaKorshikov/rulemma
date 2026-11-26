"""Audit & compile clean Russian relational Adjective -> Noun derivational table (`data/adj_noun.tsv.gz`).

Purpose:
  Equate search/SEO phrasings such as:
    - "зимние кроссовки"  <-> "кроссовки на зиму"        (зимний -> зима)
    - "детский рюкзак"    <-> "рюкзак для детей/ребенка" (детский -> ребенок)
    - "школьная форма"    <-> "форма в школу"            (школьный -> школа)
    - "рыбацкий стул"     <-> "стул для рыбалки"         (рыбацкий -> рыбалка)

Applies strict linguistic & e-commerce quality filters over manually annotated input CSV:
1. Drops malformed groups (singletons, non-Cyrillic, wrong POS) and fixes verified typos.
2. Drops qualitative adjectives mapped to abstract -ость/-есть/-ство/-ение nouns
   (e.g., умный->ум, тихий->тишина, громкий->громкость, бедный->бедность, современный->современность)
   that cause harmful false merges in search & product matching.
3. Drops verified semantic-drift & e-commerce trap pairs
   (e.g., мороженый->мороженое, трубочный->труба, ручной->рука, соленый->соль, фиговый->фига).
4. Normalizes both sides with rulemma clean lemmas and enriches missing core e-commerce relational pairs.
"""
from __future__ import annotations

import argparse
import csv
import gzip
import json
import re
from collections import Counter, defaultdict
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
DATA_DIR = ROOT / "data"

CYR_RE = re.compile(r"^[а-яё]{2,40}(?:-[а-яё]{2,25})?$")

TYPO_FIXES = {
    "катридж": "картридж",
    "утинный": "утиный",
    "белорусия": "белоруссия",
    "искуственная": "искусственный",
    "искуственный": "искусственный",
    "проволка": "проволока",
}

# Verified semantic-drift / qualitative / e-commerce collision pairs that MUST NOT fold together!
BLOCKED_PAIRS: set[tuple[str, str]] = {
    ("умный", "ум"),
    ("трубочный", "труба"),
    ("фиговый", "фига"),
    ("ручной", "рука"),            # ручной отпариватель != крем для рук
    ("мороженый", "мороженое"),    # мороженая рыба != мороженое
    ("мороженный", "мороженое"),
    ("соленый", "соль"),           # соленые огурцы != соль / соляная лампа
    ("автоматный", "автомат"),     # автоматный (оружие) != стиральная машина автомат
    ("зверский", "зверь"),
    ("судный", "суд"),
    ("дружный", "друг"),
    ("бойкий", "бой"),
    ("низкий", "низ"),
    ("новый", "новое"),
    ("новенький", "новое"),
    ("теплый", "тепло"),
    ("злой", "зло"),
    ("злобный", "зло"),
    ("сладкий", "сладость"),
    ("сладковатый", "сладость"),
    ("нормальный", "норма"),
    ("правдивый", "правда"),
    ("праведный", "правда"),
    ("правдоподобный", "правда"),
    ("социальный", "социализм"),
    ("специальный", "специалист"),
    ("рублевский", "рубль"),
    ("родной", "род"),
    ("практичный", "практика"),
    ("шумный", "шум"),
    ("шутливый", "шутка"),
    ("дырявый", "дыра"),
    ("путеводный", "путь"),
    ("благородный", "благо"),
    ("благочестивый", "благо"),
    ("явный", "явление"),
    ("чудный", "чудо"),
    ("тихий", "тишина"),
    ("громкий", "громкость"),
    ("бедный", "бедность"),
    ("богатый", "богатство"),
    ("старый", "старость"),
    ("старческий", "старость"),
    ("активный", "активность"),
    ("современный", "современность"),
    ("способный", "способность"),
    ("чувствительный", "чувство"),
    ("человеческий", "человечество"),
    ("королевский", "королевство"),
    ("царственный", "царство"),
    ("волшебный", "волшебство"),
    ("колдовской", "колдовство"),
    ("хозяйский", "хозяйство"),
    ("шкурный", "шкура"),
    ("ядовитый", "яд"),
    ("усатый", "ус"),
    ("полосатый", "полоса"),
    ("живой", "жизнь"),
    ("мертвый", "смерть"),
    ("быстрый", "быстрота"),
    ("высокий", "высота"),
    ("длинный", "длина"),
    ("широкий", "ширина"),
    ("глубокий", "глубина"),
    ("тяжелый", "тяжесть"),
    ("легкий", "легкость"),
    ("мягкий", "мягкость"),
    ("твердый", "твердость"),
    ("острый", "острота"),
    ("чистый", "чистота"),
    ("грязный", "грязь"),
    ("сухой", "сухость"),
    ("мокрый", "мокрота"),
    ("светлый", "свет"),
    ("темный", "темнота"),
    ("горячий", "жара"),
    ("холодный", "холод"),
    ("крупный", "крупность"),
    ("мелкий", "мелкость"),
    ("прочный", "прочность"),
    ("надежный", "надежность"),
    ("безопасный", "безопасность"),
    ("удобный", "удобство"),
    ("красивый", "красота"),
    ("простой", "простота"),
    ("сложный", "сложность"),
    ("точный", "точность"),
    ("сильный", "сила"),
    ("слабый", "слабость"),
    ("этот", "это"),
    ("весь", "все"),
}

# Qualitative abstract noun suffixes: reject adj->noun when noun ends in -ость/-есть/-ота/-ета/-изм
# UNLESS the noun is a real concrete domain/purpose term in CONCRETE_ALLOW_NOUNS.
ABSTRACT_SUFFIXES = ("ость", "есть", "ота", "ета")
CONCRETE_ALLOW_NOUNS = {
    "емкость", "жидкость", "крепость", "плоскость", "полость", "пряность",
    "растительность", "сладость", "местность", "поверхность", "челюсть",
    "кость", "трость", "новость", "запчасть", "часть", "масть", "пасть",
    "снасть", "власть", "страсть", "ведомость", "недвижимость", "стоимость",
    "беременность", "безопасность", "газета", "ракета", "монета", "диета",
    "котлета", "конфета", "кассета", "палета", "сигарета", "анкета", "этикетка",
    "работа", "охота", "забота", "пехота", "частота", "кислота", "ворота",
}

# High-value relational pairs guaranteed to be included (fills real e-commerce gaps in the CSV)
EXTRA_RELATIONAL: dict[str, str] = {
    "зимний": "зима",
    "летний": "лето",
    "осенний": "осень",
    "весенний": "весна",
    "детский": "ребенок",
    "мужской": "мужчина",
    "женский": "женщина",
    "подростковый": "подросток",
    "малышковый": "малыш",
    "школьный": "школа",
    "подарочный": "подарок",
    "свадебный": "свадьба",
    "рождественский": "рождество",
    "пляжный": "пляж",
    "туристический": "туризм",
    "спортивный": "спорт",
    "компьютерный": "компьютер",
    "телефонный": "телефон",
    "кухонный": "кухня",
    "ванный": "ванна",
    "постельный": "постель",
    "банный": "баня",
    "уличный": "улица",
    "комнатный": "комната",
    "садовый": "сад",
    "огородный": "огород",
    "дачный": "дача",
    "домашний": "дом",
    "офисный": "офис",
    "автомобильный": "автомобиль",
    "велосипедный": "велосипед",
    "рыбацкий": "рыбалка",
    "рыболовный": "рыбалка",
    "охотничий": "охота",
    "походный": "поход",
    "дорожный": "дорога",
    "строительный": "строительство",
    "медицинский": "медицина",
    "ветеринарный": "ветеринария",
    "кожаный": "кожа",
    "деревянный": "дерево",
    "металлический": "металл",
    "пластиковый": "пластик",
    "стеклянный": "стекло",
    "силиконовый": "силикон",
    "керамический": "керамика",
    "фарфоровый": "фарфор",
    "чугунный": "чугун",
    "стальной": "сталь",
    "алюминиевый": "алюминий",
    "золотой": "золото",
    "серебряный": "серебро",
    "хлопковый": "хлопок",
    "шерстяной": "шерсть",
    "льняной": "лен",
    "шелковый": "шелк",
    "замшевый": "замша",
    "бархатный": "бархат",
    "пуховый": "пух",
    "меховой": "мех",
    "бумажный": "бумага",
    "картонный": "картон",
    "резиновый": "резина",
    "тканевый": "ткань",
    "трикотажный": "трикотаж",
    "бамбуковый": "бамбук",
    "гранитный": "гранит",
    "мраморный": "мрамор",
    "бетонный": "бетон",
    "кирпичный": "кирпич",
    "шоколадный": "шоколад",
    "молочный": "молоко",
    "кофейный": "кофе",
    "чайный": "чай",
    "фруктовый": "фрукт",
    "овощной": "овощ",
    "ягодный": "ягода",
    "мясной": "мясо",
    "рыбный": "рыба",
    "сырный": "сыр",
    "грибной": "гриб",
    "ореховый": "орех",
    "медовый": "мед",
    "цветочный": "цветок",
    "травяной": "трава",
    "хлебный": "хлеб",
    "зерновой": "зерно",
    "кошачий": "кошка",
    "собачий": "собака",
    "птичий": "птица",
}

# Preferred canonical noun when a group has multiple nouns (e.g. дети/дитя/ребенок -> ребенок, фото/фотография -> фото)
PREFERRED_NOUN_RANK = {
    "ребенок": 100,
    "дети": 50,
    "дитя": 10,
    "фото": 100,
    "фотография": 50,
    "алкоголь": 100,
    "алко": 50,
    "профессионал": 100,
    "профи": 50,
}


def norm_yo(s: str) -> str:
    return s.replace("ё", "е").replace("Ё", "Е")


def load_lemmas(path: Path) -> dict[str, str]:
    out: dict[str, str] = {}
    with gzip.open(path, "rt", encoding="utf-8") as f:
        for line in f:
            parts = line.rstrip("\n").split("\t")
            if len(parts) >= 2:
                out[parts[0]] = parts[1]
    return out


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--csv", type=Path, required=True, help="Path to manually checked adj-noun CSV")
    parser.add_argument("--report", type=Path, default=None, help="Optional JSON audit report path")
    args = parser.parse_args()

    lemmas = load_lemmas(DATA_DIR / "lemmas.tsv.gz")
    groups: dict[str, list[tuple[str, str]]] = defaultdict(list)
    with args.csv.open(encoding="utf-8-sig") as f:
        for r in csv.DictReader(f):
            gid = r["group_id"].strip()
            w = norm_yo(r["word"].strip().lower())
            w = TYPO_FIXES.get(w, w)
            pos = r["part_of_speech"].strip().lower()
            if w and CYR_RE.fullmatch(w):
                groups[gid].append((w, pos))

    # Fix known mis-tagged POS in CSV
    if "4024" in groups:
        groups["4024"] = [("ведьмин", "adj"), ("ведьма", "noun")]

    stats = Counter()
    rejected: list[dict] = []
    adj_to_noun: dict[str, str] = {}
    noun_aliases: dict[str, str] = {}  # e.g. дитя -> ребенок, дети -> ребенок

    for gid, items in sorted(groups.items(), key=lambda kv: int(kv[0]) if kv[0].isdigit() else 999999):
        adjs = [w for w, p in items if p == "adj"]
        nouns = [w for w, p in items if p == "noun"]
        if not adjs or not nouns:
            stats["drop_malformed_group"] += 1
            rejected.append({"gid": gid, "reason": "malformed_group", "items": items})
            continue

        # Normalize nouns via rulemma clean dictionary (so руны->руна, дети->ребенок, etc.)
        norm_nouns = []
        for n in nouns:
            nl = lemmas.get(n, n)
            # Keep noun if rulemma maps it to a valid noun form (not an adj/verb)
            if nl.endswith(("ый", "ий", "ой", "ть", "ться")) and not n.endswith(("ый", "ий", "ой", "ть", "ться")):
                nl = n
            norm_nouns.append(nl)
        # Pick best canonical noun in group
        canon_noun = max(
            set(norm_nouns),
            key=lambda x: (PREFERRED_NOUN_RANK.get(x, 0), 1 if x in lemmas else 0, -len(x)),
        )
        # Record extra noun synonyms inside multi-noun groups (e.g. дитя -> ребенок)
        for nn in set(norm_nouns):
            if nn != canon_noun and len(nn) >= 3:
                noun_aliases[nn] = canon_noun

        for a in adjs:
            al = lemmas.get(a, a)
            # Keep adjective form if rulemma lemmatized a participle to a verb (e.g. ламинированный -> ламинировать)
            if al.endswith(("ть", "ться", "ти", "чь")) and a.endswith(("ый", "ий", "ой")):
                al = a

            if al == canon_noun:
                stats["drop_identity"] += 1
                continue
            if (al, canon_noun) in BLOCKED_PAIRS or (a, canon_noun) in BLOCKED_PAIRS:
                stats["drop_blocked_semantic_drift"] += 1
                rejected.append({"gid": gid, "reason": "blocked_semantic_drift", "adj": al, "noun": canon_noun})
                continue
            if len(canon_noun) <= 2 or len(al) <= 3:
                stats["drop_too_short"] += 1
                rejected.append({"gid": gid, "reason": "too_short", "adj": al, "noun": canon_noun})
                continue
            if any(canon_noun.endswith(s) for s in ABSTRACT_SUFFIXES) and canon_noun not in CONCRETE_ALLOW_NOUNS:
                stats["drop_abstract_noun"] += 1
                rejected.append({"gid": gid, "reason": "abstract_noun", "adj": al, "noun": canon_noun})
                continue
            # Reject verbal-participle adjectives ending in -ущий/-ющий/-ащий/-ящий/-вший (e.g. очищающий, отбеливающий)
            if al.endswith(("ущий", "ющий", "ащий", "ящий", "вший")):
                stats["drop_active_participle"] += 1
                rejected.append({"gid": gid, "reason": "active_participle", "adj": al, "noun": canon_noun})
                continue

            if al not in adj_to_noun:
                adj_to_noun[al] = canon_noun
                stats["kept_from_csv"] += 1
            else:
                stats["dup_adj_skipped"] += 1

    # Merge guaranteed curated e-commerce relational pairs (highest priority)
    extra_added = 0
    for a, n in EXTRA_RELATIONAL.items():
        if a not in adj_to_noun:
            extra_added += 1
        adj_to_noun[a] = n
    stats["curated_extra_added"] = extra_added

    # Also keep explicit noun->noun group aliases (e.g. дитя -> ребенок, фотография -> фото? No, keep only safe дитя -> ребенок!)
    safe_noun_aliases = {"дитя": "ребенок", "ребятишки": "ребенок"}
    for k, v in safe_noun_aliases.items():
        adj_to_noun[k] = v

    out_path = DATA_DIR / "adj_noun.tsv.gz"
    with gzip.open(out_path, "wt", encoding="utf-8", compresslevel=6) as f:
        for a, n in sorted(adj_to_noun.items()):
            f.write(f"{a}\t{n}\n")

    report = {
        "input_groups": len(groups),
        "output_pairs": len(adj_to_noun),
        "output_kb": round(out_path.stat().st_size / 1024, 1),
        "stats": dict(stats),
        "rejected_total": len(rejected),
        "rejected_sample": rejected[:60],
    }
    if args.report:
        args.report.parent.mkdir(parents=True, exist_ok=True)
        args.report.write_text(json.dumps(report, ensure_ascii=False, indent=2), encoding="utf-8")
    print("[adj_noun] Done!", json.dumps({k: v for k, v in report.items() if k != "rejected_sample"}, ensure_ascii=False, indent=2))


if __name__ == "__main__":
    main()
