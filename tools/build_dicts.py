"""Build clean, compact open-source dictionaries (.tsv.gz) for rulemma.

Inputs:
- raw_dicts/lemmas.json, adj_forms.json, types.json
- Optional --extra-words-tsv (frequency-ordered Russian words to enrich dictionary coverage,
  only plain Cyrillic/hyphen words — zero proprietary IDs or URLs)
- data/overrides.tsv (highest-priority human-editable overrides)

Applies principled linguistic fixes over pymorphy3 / OpenCorpora:
1. OOV hallucination guard: never turn unknown words into fake VERB/INFN/GRND/ADJS,
   fake Name/Surn/Geox, fake feminine genitive plural (+а/+я/+ин), or -и -> -ь.
2. Short-token & OpenCorpora artifact guards (v0.2.2):
   - Exhaustive allowlist for 2-letter Russian grammar forms (protects см, ок, ср, вв, ам, ат, ив, etc.);
   - Guard against fake Name plur,gent (+а/+я, e.g. шакл->шакла, лайн->лайна, айс->айса, лит->лита)
     and short Infr plur,gent (обж->обжа).
3. Known-word disambiguation: prefer inanimate NOUN,sing,nomn over Surn/Name/Geox,
   rare imperative VERB/GRND/ADJS, or low-margin genitive collisions while preserving
   real inflections (комаров->комар, зубов->зуб, газа->газ, белые->белый).
4. Full adjectival participles (PRTF, v0.2.2): map full participles to their masculine singular
   nominative adjectival form (увлажняющая->увлажняющий, утепленные->утепленный, встроенная->встроенный,
   оцинкованные->оцинкованный) instead of verb infinitives, and enrich adj_forms.tsv.gz with their
   masc/femn/neut/plur agreement paradigms while leaving verbs, PRTS, GRND, and nouns intact.
5. Adjective normalization & human overrides from data/overrides.tsv always win last.

Outputs in data/:
- data/lemmas.tsv.gz
- data/adj_forms.tsv.gz
- data/types.tsv.gz
- data/morph.tsv.gz        (v0.3: compact 32-bit grammatical tag bitmask per known wordform)
- data/alt_lemmas.tsv.gz   (v0.3: alternative (lemma, POS, cases) parses for homonymous forms)
- data/paradigms.tsv.gz    (v0.3: deduplicated suffix inflection paradigms for NOUN & ADJF/PRTF)
- data/build_report.json
"""
from __future__ import annotations

import argparse
import gzip
import json
import re
import time
from collections import Counter, defaultdict
from pathlib import Path

import pymorphy3

ROOT = Path(__file__).resolve().parents[1]
RAW_DIR = ROOT / "raw_dicts"
DATA_DIR = ROOT / "data"

CYR_WORD_RE = re.compile(r"^[а-яё]{2,40}(?:-[а-яё]{2,25})?$")
CYR_OVERRIDE_RE = re.compile(r"^[а-яё]{1,40}(?:-[а-яё]{1,25})?$")
CONSONANTS = set("бвгджзйклмнпрстфхцчшщ")
VOWELS = set("аеёиоуыэюя")

# Exhaustive Russian 2-letter inflected grammar forms that legitimately map to another lemma.
# Any other 2-letter Cyrillic token (units, abbreviations, short words like см, ок, ср, вв, ам, ат, ив)
# stays untouched instead of triggering archaic OpenCorpora plur,gent or verb entries.
TWO_LETTER_GRAMMAR_ALLOW: dict[str, str] = {
    "во": "в",
    "со": "с",
    "ко": "к",
    "об": "о",
    "ее": "она",
    "её": "она",
    "ей": "она",
    "ею": "она",
    "им": "они",
    "их": "они",
    "та": "тот",
    "те": "тот",
    "ту": "тот",
    "ел": "есть",
    "ем": "есть",
    "чо": "что",
    "чё": "что",
    "шо": "что",
    "юн": "юный",
    "гг": "год",
}

REQUIRED_ADJ_FORMS: dict[str, str] = {
    "оцинкованный||plur": "оцинкованные",
    "орнаментированный||plur": "орнаментированные",
    "встраиваемый||plur": "встраиваемые",
    "оксидированный||plur": "оксидированные",
    "самолигирующий|masc|sing": "самолигирующий",
    "самолигирующий|femn|sing": "самолигирующая",
    "самолигирующий|neut|sing": "самолигирующее",
    "самолигирующий||plur": "самолигирующие",
}

# Normative stressed -ой adjectives that OpenCorpora / raw adj_forms default to archaic/homonymous -ый.
PREFER_OY_ADJS: dict[str, str] = {
    "беспроводный": "беспроводной",
    "складный": "складной",
    "переносный": "переносной",
    "навесный": "навесной",
    "запасный": "запасной",
    "накладный": "накладной",
    "заливный": "заливной",
    "разливный": "разливной",
    "отварный": "отварной",
}

# Common Russian base lemmas missing from raw product-only dumps so dictionary-verified
# suffix fallback can inflect all their oblique forms cleanly without bloating RAM.
CORE_EXTRA_WORDS: tuple[str, ...] = (
    "трудоустройство", "последующий", "утильсбор", "растаможка", "дальнейший",
    "оквэд", "высокооплачиваемый", "пошлина", "ввоз", "таможенный",
    "многодетный", "логист", "консультационный", "тестировщик", "автоаукцион",
    "сотрясение", "втроем", "ноотропный", "подливка", "панировочный",
    "шлепанец", "учредительный", "самолигирующий", "перекрашивать",
    "чешка", "колготки", "санки", "хлопья", "чипсы", "джоггер",
    "кофемашина", "мфу",
)

# Common Russian product types missing or lacking gender/number in raw types.json.
REQUIRED_TYPES: dict[str, tuple[str, str]] = {
    "кофемашина": ("femn", "sing"),
    "мфу": ("neut", "sing"),
    "машина": ("femn", "sing"),
    "стиральная машина": ("femn", "sing"),
    "посудомоечная машина": ("femn", "sing"),
    "сушильная машина": ("femn", "sing"),
    "швейная машина": ("femn", "sing"),
    "брекеты": ("", "plur"),
    "духи": ("", "plur"),
    "трусы": ("", "plur"),
    "носки": ("", "plur"),
    "колготки": ("", "plur"),
    "очки": ("", "plur"),
    "часы": ("", "plur"),
    "весы": ("", "plur"),
    "наушники": ("", "plur"),
    "щипцы": ("", "plur"),
    "бокорезы": ("", "plur"),
    "ходунки": ("", "plur"),
    "сланцы": ("", "plur"),
    "берцы": ("", "plur"),
    "угги": ("", "plur"),
    "лосины": ("", "plur"),
    "тайтсы": ("", "plur"),
}

# Major Russian hyphenated cities & compound toponyms (all standard oblique cases) so Word()
# resolves them before splitting on '-' and never inflects indeclinable parts like "-на-дону".
COMPOUND_TOPONYM_FORMS: dict[str, str] = {
    # Ростов-на-Дону
    "ростов-на-дону": "ростов-на-дону",
    "ростова-на-дону": "ростов-на-дону",
    "ростову-на-дону": "ростов-на-дону",
    "ростовом-на-дону": "ростов-на-дону",
    "ростове-на-дону": "ростов-на-дону",
    # Комсомольск-на-Амуре
    "комсомольск-на-амуре": "комсомольск-на-амуре",
    "комсомольска-на-амуре": "комсомольск-на-амуре",
    "комсомольску-на-амуре": "комсомольск-на-амуре",
    "комсомольском-на-амуре": "комсомольск-на-амуре",
    "комсомольске-на-амуре": "комсомольск-на-амуре",
    # Южно-Сахалинск
    "южно-сахалинск": "южно-сахалинск",
    "южно-сахалинска": "южно-сахалинск",
    "южно-сахалинску": "южно-сахалинск",
    "южно-сахалинском": "южно-сахалинск",
    "южно-сахалинске": "южно-сахалинск",
    # Йошкар-Ола
    "йошкар-ола": "йошкар-ола",
    "йошкар-олы": "йошкар-ола",
    "йошкар-оле": "йошкар-ола",
    "йошкар-олу": "йошкар-ола",
    "йошкар-олой": "йошкар-ола",
    "йошкар-олою": "йошкар-ола",
    # Санкт-Петербург
    "санкт-петербург": "санкт-петербург",
    "санкт-петербурга": "санкт-петербург",
    "санкт-петербургу": "санкт-петербург",
    "санкт-петербургом": "санкт-петербург",
    "санкт-петербурге": "санкт-петербург",
    # Улан-Удэ
    "улан-удэ": "улан-удэ",
    # Орехово-Зуево
    "орехово-зуево": "орехово-зуево",
    "орехово-зуева": "орехово-зуево",
    "орехово-зуеву": "орехово-зуево",
    "орехово-зуевом": "орехово-зуево",
    "орехово-зуеве": "орехово-зуево",
    # Петропавловск-Камчатский
    "петропавловск-камчатский": "петропавловск-камчатский",
    "петропавловска-камчатского": "петропавловск-камчатский",
    "петропавловску-камчатскому": "петропавловск-камчатский",
    "петропавловском-камчатским": "петропавловск-камчатский",
    "петропавловске-камчатском": "петропавловск-камчатский",
    # Каменск-Уральский
    "каменск-уральский": "каменск-уральский",
    "каменска-уральского": "каменск-уральский",
    "каменску-уральскому": "каменск-уральский",
    "каменском-уральским": "каменск-уральский",
    "каменске-уральском": "каменск-уральский",
    # Каменск-Шахтинский
    "каменск-шахтинский": "каменск-шахтинский",
    "каменска-шахтинского": "каменск-шахтинский",
    "каменску-шахтинскому": "каменск-шахтинский",
    "каменском-шахтинским": "каменск-шахтинский",
    "каменске-шахтинском": "каменск-шахтинский",
    # Ленинск-Кузнецкий
    "ленинск-кузнецкий": "ленинск-кузнецкий",
    "ленинска-кузнецкого": "ленинск-кузнецкий",
    "ленинску-кузнецкому": "ленинск-кузнецкий",
    "ленинском-кузнецким": "ленинск-кузнецкий",
    "ленинске-кузнецком": "ленинск-кузнецкий",
    # Кирово-Чепецк
    "кирово-чепецк": "кирово-чепецк",
    "кирово-чепецка": "кирово-чепецк",
    "кирово-чепецку": "кирово-чепецк",
    "кирово-чепецком": "кирово-чепецк",
    "кирово-чепецке": "кирово-чепецк",
    # Переславль-Залесский
    "переславль-залесский": "переславль-залесский",
    "переславля-залесского": "переславль-залесский",
    "переславлю-залесскому": "переславль-залесский",
    "переславлем-залесским": "переславль-залесский",
    "переславле-залесском": "переславль-залесский",
    # Ханты-Мансийск
    "ханты-мансийск": "ханты-мансийск",
    "ханты-мансийска": "ханты-мансийск",
    "ханты-мансийску": "ханты-мансийск",
    "ханты-мансийском": "ханты-мансийск",
    "ханты-мансийске": "ханты-мансийск",
    # Горно-Алтайск
    "горно-алтайск": "горно-алтайск",
    "горно-алтайска": "горно-алтайск",
    "горно-алтайску": "горно-алтайск",
    "горно-алтайском": "горно-алтайск",
    "горно-алтайске": "горно-алтайск",
    # Наро-Фоминск
    "наро-фоминск": "наро-фоминск",
    "наро-фоминска": "наро-фоминск",
    "наро-фоминску": "наро-фоминск",
    "наро-фоминском": "наро-фоминск",
    "наро-фоминске": "наро-фоминск",
    # Славянск-на-Кубани
    "славянск-на-кубани": "славянск-на-кубани",
    "славянска-на-кубани": "славянск-на-кубани",
    "славянску-на-кубани": "славянск-на-кубани",
    "славянском-на-кубани": "славянск-на-кубани",
    "славянске-на-кубани": "славянск-на-кубани",
    # Гусь-Хрустальный
    "гусь-хрустальный": "гусь-хрустальный",
    "гуся-хрустального": "гусь-хрустальный",
    "гусю-хрустальному": "гусь-хрустальный",
    "гусем-хрустальным": "гусь-хрустальный",
    "гусе-хрустальном": "гусь-хрустальный",
    # Усть-Илимск
    "усть-илимск": "усть-илимск",
    "усть-илимска": "усть-илимск",
    "усть-илимску": "усть-илимск",
    "усть-илимском": "усть-илимск",
    "усть-илимске": "усть-илимск",
    # Соль-Илецк
    "соль-илецк": "соль-илецк",
    "соль-илецка": "соль-илецк",
    "соль-илецку": "соль-илецк",
    "соль-илецком": "соль-илецк",
    "соль-илецке": "соль-илецк",
}

COMMON_VERB_ALLOW = {
    "купить", "покупать", "заказать", "выбрать", "смотреть", "посмотреть", "скачать",
    "установить", "подключить", "настроить", "починить", "заменить", "снять", "надеть",
    "продать", "сдать", "открыть", "закрыть", "включить", "выключить", "помыть", "стирать",
    "сушить", "гладить", "варить", "жарить", "печь", "резать", "пилить", "сверлить",
    "красить", "клеить", "шить", "вязать", "читать", "писать", "играть", "бегать", "носить",
    "лечить", "мазать", "пить", "есть", "спать", "гулять", "ехать", "лететь", "плыть",
    "сравнить", "проверить", "узнать", "найти", "подобрать", "рассчитать", "подарить",
    "работает", "работать", "включается", "включаться", "заряжается", "заряжаться",
}


def norm_yo(s: str) -> str:
    return s.replace("ё", "е").replace("Ё", "Е")


def is_in_opencorpora(parse) -> bool:
    """True ONLY when every method in the parse stack is a real dictionary lookup (never UnknownPrefix/FakeDictionary)."""
    return bool(parse.methods_stack) and all(
        m[0].__class__.__name__ in ("DictionaryAnalyzer", "KnownPrefixAnalyzer")
        for m in parse.methods_stack
    )


def has_pure_dict_parse(parse) -> bool:
    return bool(parse.methods_stack) and all(
        m[0].__class__.__name__ == "DictionaryAnalyzer" for m in parse.methods_stack
    )


def load_overrides(path: Path) -> dict[str, str]:
    ov: dict[str, str] = {}
    if not path.exists():
        return ov
    for line in path.read_text(encoding="utf-8").splitlines():
        line = line.strip()
        if not line or line.startswith("#"):
            continue
        parts = line.split("\t")
        if len(parts) >= 2:
            w = parts[0].strip().lower()
            l = norm_yo(parts[1].strip().lower())
            if w and l:
                ov[w] = l
                wn = norm_yo(w)
                if wn != w:
                    ov[wn] = l
    return ov


def collect_prtf_adj_forms(parse, lemma_norm: str, extra_adj_forms: dict[str, str]) -> None:
    """Populate masc/femn/neut/plur agreement forms for an adjectival participle (PRTF)."""
    specs = (
        ("masc|sing", {"masc", "sing", "nomn"}),
        ("femn|sing", {"femn", "sing", "nomn"}),
        ("neut|sing", {"neut", "sing", "nomn"}),
        ("|plur", {"plur", "nomn"}),
    )
    for suffix, grams in specs:
        key = f"{lemma_norm}|{suffix}"
        if key in extra_adj_forms:
            continue
        inf = parse.inflect(grams)
        if inf and inf.word:
            extra_adj_forms[key] = inf.word.lower()


def smart_lemmatize(
    word: str,
    morph: pymorphy3.MorphAnalyzer,
    adj_form_to_lemmas: dict[str, set[str]],
    extra_adj_forms: dict[str, str],
    stats: Counter,
) -> str:
    w = word.lower()
    wn = norm_yo(w)

    # Rule 0: Single-letter & 2-letter allowlist guard
    if len(wn) == 1:
        stats["guard_1_char"] += 1
        return wn
    if len(wn) == 2:
        if w in TWO_LETTER_GRAMMAR_ALLOW:
            stats["allow_2_char"] += 1
            return TWO_LETTER_GRAMMAR_ALLOW[w]
        stats["guard_2_char"] += 1
        return wn

    # Rule 3: Adjective forms from curated adj_forms.json ONLY when word actually parses as ADJF/PRTF
    # and is NOT primarily a common nominative inanimate noun (prevents сад->садов, град->градов, запад->западов, мороженое->мороженый!)
    adj_cands = adj_form_to_lemmas.get(w) or adj_form_to_lemmas.get(wn)
    parses = morph.parse(w)
    if adj_cands and parses:
        top0 = parses[0]
        is_common_noun = (
            has_pure_dict_parse(top0)
            and top0.tag.POS == "NOUN"
            and "inan" in top0.tag
            and "nomn" in top0.tag
            and not any(g in top0.tag for g in ("Surn", "Name", "Patr", "Geox"))
            and top0.score >= 0.35
        )
        if not is_common_noun:
            for p in parses:
                if p.tag.POS in ("ADJF", "PRTF") and p.score >= 0.08:
                    nf = norm_yo(p.normal_form)
                    if nf in adj_cands:
                        stats["rule_adj_form"] += 1
                        return nf
                    if p.tag.POS == "PRTF":
                        inf = p.inflect({"masc", "sing", "nomn"})
                        if inf and norm_yo(inf.word) in adj_cands:
                            stats["rule_adj_form"] += 1
                            return norm_yo(inf.word)

    if not parses:
        return wn

    top = parses[0]
    top_nf = norm_yo(top.normal_form)
    top_known = is_in_opencorpora(top)

    # Rule 4 (v0.2.2): Full adjectival participles (PRTF) -> masculine singular nominative form
    # (e.g. увлажняющая->увлажняющий, утепленные->утепленный, встроенная->встроенный, оцинкованные->оцинкованный).
    # Leaves verbs (VERB/INFN), short participles (PRTS), gerunds (GRND), and common nouns untouched.
    if top_known and top.tag.POS == "PRTF":
        has_strong_noun = any(
            is_in_opencorpora(p)
            and p.tag.POS == "NOUN"
            and "nomn" in p.tag
            and not any(g in p.tag for g in ("Surn", "Name", "Patr", "Geox"))
            and p.score >= 0.25
            for p in parses
        )
        if not has_strong_noun:
            inf = top.inflect({"masc", "sing", "nomn"})
            if inf and inf.word:
                prtf_lemma = norm_yo(inf.word.lower())
                collect_prtf_adj_forms(top, prtf_lemma, extra_adj_forms)
                stats["fix_prtf_to_adj"] += 1
                return prtf_lemma

    # If top parse already equals word, keep it — EXCEPT when top is a tied Surn (-ов/-ев)
    # colliding with a real common NOUN genitive plural (e.g. рубцов->рубец, усов->ус, горшков->горшок, рогов->рог).
    if top_nf == wn and not (top_known and "Surn" in top.tag and top.score <= 0.55):
        stats["identity"] += 1
        return wn

    # Rule 1: OOV Predictor Guards (not pure DictionaryAnalyzer / KnownPrefixAnalyzer!)
    if not top_known:
        # 1a. Archaic pre-1918 feminine plural -ыя/-ия hallucination for -ый/-ий adjectives
        if wn.endswith(("ый", "ий", "ой")) and top_nf.endswith(("ыя", "ия")):
            stats["fix_oov_archaic_adj"] += 1
            return wn
        if wn.endswith(("ые", "ие")) and top_nf.endswith(("ыя", "ия")):
            stats["fix_oov_archaic_adj"] += 1
            return wn[:-2] + ("ый" if wn.endswith("ые") else "ий")

        # 1b. Never hallucinate verbs/participles/short-adjs/gerunds for unknown words
        if top.tag.POS in ("VERB", "INFN", "GRND", "PRTF", "PRTS", "ADJS"):
            stats["fix_oov_verb_adjs"] += 1
            return wn

        # 1c. Never hallucinate fake Name/Surn/Patr/Geox nominative/genitive shifts
        if any(g in top.tag for g in ("Name", "Surn", "Patr", "Geox")):
            if top_nf == wn + "а" or top_nf == wn + "я" or (wn.endswith("а") and top_nf == wn[:-1]):
                stats["fix_oov_propn"] += 1
                return wn

        # 1d. Never hallucinate genitive plural adding -а/-я/-ин, fleeting-vowel femn -а, -и -> -ь, or stripping -а
        if "gent" in top.tag or "plur" in top.tag:
            if wn[-1] in (CONSONANTS | {"ь"}) and (
                top_nf == wn + "а"
                or top_nf == wn + "я"
                or top_nf == wn + "ин"
                or ("femn" in top.tag and top_nf.endswith(("а", "я")) and len(wn) >= 4)
            ):
                stats["fix_oov_fake_femn_gent"] += 1
                return wn
            if wn[-1] in ("о", "у", "э", "ю"):
                stats["fix_oov_indecl"] += 1
                return wn
            if wn.endswith("и") and top_nf.endswith("ь"):
                stats["fix_oov_indecl"] += 1
                return wn
            if wn.endswith("а") and len(wn) >= 4 and top_nf == wn[:-1]:
                stats["fix_oov_strip_a"] += 1
                return wn

        # 1e. OOV ending in -о/-у/-э/-ю should stay as-is
        if wn[-1] in ("о", "у", "э", "ю"):
            stats["fix_oov_indecl"] += 1
            return wn

        stats["oov_regular_inflect"] += 1
        return top_nf

    # Rule 2: Known OpenCorpora Word Disambiguation
    # 2.0a (v0.2.2): Guard against fake given-name plur,gent (+а/+я) & short Name oblique collisions
    # (e.g. шакл->шакла, лайн->лайна, айс->айса, лит->лита, рип->рипа, тай->тая, изи->изя, абрамов->абрам).
    if "Name" in top.tag and (
        ("plur" in top.tag and "gent" in top.tag)
        or (len(wn) <= 3 and "nomn" not in top.tag)
    ):
        alt_inan = [
            p for p in parses
            if is_in_opencorpora(p)
            and p.tag.POS == "NOUN"
            and "inan" in p.tag
            and not any(g in p.tag for g in ("Name", "Surn", "Patr", "Geox"))
            and p.score >= 0.15
        ]
        stats["fix_known_name_plur_gent"] += 1
        if alt_inan:
            alt_nfs = [norm_yo(p.normal_form) for p in alt_inan]
            if wn in alt_nfs:
                return wn
            if top_nf in alt_nfs:
                return top_nf
            cand = alt_nfs[0]
            if cand not in (wn + "ы", wn + "и", wn + "а", wn + "я"):
                return cand
        return wn

    # 2.0b (v0.2.2): Short colloquial/archaic Infr plur,gent (e.g. обж->обжа, сот->сотня)
    if len(wn) <= 3 and "Infr" in top.tag and "plur" in top.tag and "gent" in top.tag:
        stats["fix_known_short_infr"] += 1
        return wn

    # Keep high-confidence full adjectives as adjectives (детская->детский, красная->красный)
    if top.tag.POS == "ADJF" and top.score >= 0.35:
        stats["known_adj"] += 1
        return top_nf

    # Check if word itself has a clean inanimate NOUN,sing,nomn dictionary parse
    nom_inan = [
        p for p in parses
        if is_in_opencorpora(p)
        and p.tag.POS == "NOUN"
        and "inan" in p.tag
        and "sing" in p.tag
        and "nomn" in p.tag
        and not any(g in p.tag for g in ("Surn", "Name", "Patr", "Geox"))
        and norm_yo(p.normal_form) == wn
    ]
    if nom_inan:
        best_nom = nom_inan[0]
        # 2a. Inanimate nominative noun beats Surn/Name/Patr/Geox (майка->майк, планка->планк, астра->астр, туя->туй)
        if any(g in top.tag for g in ("Surn", "Name", "Patr", "Geox")) and best_nom.score >= 0.05:
            stats["fix_known_vs_propn"] += 1
            return wn
        # 2b. Inanimate nominative noun beats rare VERB/GRND/ADJS/NPRO (спрей->спреть, петля->петлить, поло->полый, сип->сипнуть)
        if top.tag.POS in ("VERB", "GRND", "ADJS", "NPRO") and wn not in COMMON_VERB_ALLOW and best_nom.score >= 0.05:
            stats["fix_known_vs_verb_adjs"] += 1
            return wn
        # 2c. Inanimate nominative noun beats genitive collision when nomn has solid probability (математика, кружка, гарнитур, душ, калина)
        if "gent" in top.tag and best_nom.score >= 0.12 and top.score <= 0.75:
            if not (top.score >= 0.65 and "masc" in top.tag and "inan" in top.tag and "sing" in top.tag):
                stats["fix_known_vs_gent"] += 1
                return wn

    # Preserve -нье/-тье/-лье/-дье spelling variants (печенье, варенье, сиденье, платье)
    if wn.endswith(("нье", "тье", "лье", "дье", "вье", "жье", "чье")) and top_nf == wn[:-2] + "ие":
        stats["fix_nye_spelling"] += 1
        return wn

    # Rules 2d / 2e / 2f (v0.2.2, ANY word length): oblique/plural common NOUN vs ADJS, VERB(impr)/GRND, and PropN
    common_nouns = [
        p for p in parses
        if is_in_opencorpora(p)
        and p.tag.POS == "NOUN"
        and not any(g in p.tag for g in ("Name", "Surn", "Patr", "Geox", "Infr", "Arch"))
        and norm_yo(p.normal_form) != top_nf
    ]
    if common_nouns:
        bn = common_nouns[0]
        bn_nf = norm_yo(bn.normal_form)
        # 2d. Short adjective ADJS vs common NOUN at tied/near-tied score
        # (e.g. ковров->ковер, шлангов->шланг, румяна->румяна, саженцев->саженец, тортов->торт, чаем->чай, мяты->мята)
        if top.tag.POS == "ADJS" and bn.score >= top.score * 0.85:
            if not ("Pltm" in bn.tag and bn_nf != wn):
                stats["fix_known_adjs_vs_noun"] += 1
                return bn_nf
        # 2e. Rare imperative VERB or gerund GRND vs common NOUN at tied/near-tied score
        # (e.g. мочи->моча, сколов->скол, голубей->голубь, лишая->лишай, тли->тля, вшей->вошь, щелочи->щелочь, слесарь->слесарь)
        if (
            ((top.tag.POS == "VERB" and "impr" in top.tag) or top.tag.POS == "GRND")
            and wn not in COMMON_VERB_ALLOW
            and bn.score >= top.score * 0.85
        ):
            stats["fix_known_impr_grnd_vs_noun"] += 1
            return bn_nf
        # 2f. Oblique Name/Surn/Patr/Geox (never Fixd) or tied Surn(-ов/-ев) vs common NOUN
        # (e.g. саморезы->саморез, цветами->цвет, альфу->альфа, турбину->турбина, свинины->свинина,
        #  рубцов->рубец, горшков->горшок, ульи->улей, газе->газ, косу->коса, пиле->пила, тую->туя)
        if (
            any(g in top.tag for g in ("Name", "Surn", "Patr", "Geox"))
            and "Fixd" not in top.tag
            and bn.score >= top.score * 0.85
        ):
            is_oblique_propn = (top_nf != wn)
            is_tied_surn = ("Surn" in top.tag and top_nf == wn and top.score <= 0.55)
            if (is_oblique_propn or is_tied_surn) and not ("Pltm" in bn.tag and bn_nf != wn):
                stats["fix_known_propn_vs_noun"] += 1
                return bn_nf

    stats["known_default"] += 1
    return top_nf


def write_tsv_gz(path: Path, rows: list[tuple[str, ...]]) -> None:
    with gzip.open(path, "wt", encoding="utf-8", compresslevel=6) as f:
        for row in rows:
            f.write("\t".join(row) + "\n")


def clean_types_keys(raw_types: dict) -> list[str]:
    return [norm_yo(str(k).lower()) for k in raw_types if k] + list(REQUIRED_TYPES)


# --- v0.3 Compact Morphology (morph.tsv.gz + alt_lemmas.tsv.gz) & Paradigms (paradigms.tsv.gz) ---

POS_CODES: dict[str, int] = {
    "": 0,
    "NOUN": 1, "ADJF": 2, "ADJS": 3, "COMP": 4,
    "VERB": 5, "INFN": 6, "PRTF": 7, "PRTS": 8, "GRND": 9,
    "NUMR": 10, "ADVB": 11, "NPRO": 12, "PRED": 13,
    "PREP": 14, "CONJ": 15, "PRCL": 16, "INTJ": 17,
}
GENDER_CODES: dict[str, int] = {"": 0, "masc": 1, "femn": 2, "neut": 3}
NUMBER_CODES: dict[str, int] = {"": 0, "sing": 1, "plur": 2, "both": 3}

CASE_ORDER: tuple[str, ...] = ("nomn", "gent", "datv", "accs", "ablt", "loct")
CASE_BIT_OFFSET = 9
CASE_MASK_ALL = 0x3F  # 6 cases
CASE_TO_6BIT: dict[str, int] = {
    "nomn": 1 << 0,
    "gent": 1 << 1, "gen2": 1 << 1,
    "datv": 1 << 2,
    "accs": 1 << 3,
    "ablt": 1 << 4,
    "loct": 1 << 5, "loc2": 1 << 5,
}
GOVERNED_POS_SET: frozenset[str] = frozenset({"NOUN", "ADJF", "NPRO", "PRTF", "NUMR"})

FLAG_ANIM          = 1 << 15
FLAG_INAN          = 1 << 16
FLAG_PLTM          = 1 << 17
FLAG_SGTM          = 1 << 18
FLAG_FIXD          = 1 << 19
FLAG_HAS_NOUN      = 1 << 20
FLAG_HAS_ADJ       = 1 << 21
FLAG_HAS_VERB      = 1 << 22
FLAG_HAS_GOVERNED  = 1 << 23
FLAG_HAS_NOMN_NOUN = 1 << 24
FLAG_HAS_PROPN     = 1 << 25

# Strict pluralia tantum nouns (no valid singular form) so Inflect/AgreeNumber always guard Pltm.
STRICT_PLTM_LEMMAS: frozenset[str] = frozenset({
    "ножницы", "духи", "брекеты", "колготки", "трусы", "щипцы", "бокорезы",
    "ходунки", "сливки", "санки", "хлопья", "чипсы", "очки", "часы", "весы",
    "брюки", "джинсы", "шорты", "плоскогубцы", "пассатижи", "кусачки",
    "грабли", "вилы", "ворота", "деньги", "сутки", "обои", "шахматы",
    "шашки", "качели", "перила", "чернила", "дрожжи", "макароны",
    "консервы", "переговоры", "финансы", "каникулы", "сумерки", "хлопоты",
    "сланцы", "берцы", "угги", "лосины", "тайтсы",
})

# One-letter Russian service words guaranteed to carry accurate POS in morph.tsv.gz.
SINGLE_LETTER_WORDS: tuple[str, ...] = ("в", "с", "к", "о", "у", "а", "и", "я")

# Paradigm word flags (uint16)
PF_NOUN = 1
PF_ADJ  = 2
PF_ANIM = 1 << 4
PF_INAN = 1 << 5
PF_PLTM = 1 << 6
PF_SGTM = 1 << 7
PF_FIXD = 1 << 8


def _prtf_masc_lemma(parse) -> str:
    inf = parse.inflect({"masc", "sing", "nomn"})
    if inf and inf.word:
        return norm_yo(inf.word.lower())
    return norm_yo(parse.normal_form.lower())


def _parse_cand_lemma(parse) -> str:
    if parse.tag.POS == "PRTF":
        return _prtf_masc_lemma(parse)
    return norm_yo(parse.normal_form.lower())


def _lcp_len(strings: list[str]) -> int:
    if not strings:
        return 0
    s0 = strings[0]
    end = len(s0)
    for s in strings[1:]:
        if len(s) < end:
            end = len(s)
        i = 0
        while i < end and s0[i] == s[i]:
            i += 1
        end = i
        if end == 0:
            return 0
    return end


def build_morph_and_alt_lemmas(
    clean_lemmas: dict[str, str],
    clean_types: dict[str, tuple[str, str]],
    morph: pymorphy3.MorphAnalyzer,
) -> tuple[dict[str, int], dict[str, str]]:
    """Build compact uint32 morphology table and homonym alternative-lemmas table for known wordforms."""
    morph_map: dict[str, int] = {}
    alt_map: dict[str, str] = {}

    all_words = sorted(set(clean_lemmas.keys()) | set(SINGLE_LETTER_WORDS))
    for w in all_words:
        lm = clean_lemmas.get(w, norm_yo(w))
        parses = morph.parse(w)
        known = [p for p in parses if is_in_opencorpora(p)]

        if not known:
            # Fallback to curated types.tsv.gz if available (e.g. кофемашина, мфу)
            tinfo = clean_types.get(w) or clean_types.get(lm)
            if tinfo is not None and tinfo[1]:
                g_code = GENDER_CODES.get(tinfo[0], 0)
                n_code = NUMBER_CODES.get(tinfo[1], 0)
                bits = (
                    POS_CODES["NOUN"]
                    | (g_code << 5)
                    | (n_code << 7)
                    | (CASE_TO_6BIT["nomn"] << CASE_BIT_OFFSET)
                    | FLAG_INAN
                    | FLAG_HAS_NOUN
                    | FLAG_HAS_GOVERNED
                    | FLAG_HAS_NOMN_NOUN
                )
                if lm in STRICT_PLTM_LEMMAS:
                    bits |= FLAG_PLTM
                morph_map[w] = bits
            continue

        # Choose primary parse consistent with rulemma's canonical lemma lm
        primary = None
        for p in known:
            if _parse_cand_lemma(p) == lm:
                primary = p
                break
        if primary is None:
            for p in known:
                if p.tag.POS == "NOUN" and "Pltm" in p.tag and norm_yo(p.normal_form) == lm:
                    primary = p
                    break
        if primary is None:
            primary = known[0]

        pos_str = str(primary.tag.POS or "")
        pos_code = POS_CODES.get(pos_str, 0)
        gen_code = GENDER_CODES.get(str(primary.tag.gender or ""), 0)

        # Detect number across parses matching primary lemma & POS
        nums_seen: set[str] = set()
        for p in known:
            if str(p.tag.POS or "") == pos_str and _parse_cand_lemma(p) == lm:
                if p.tag.number in ("sing", "plur"):
                    nums_seen.add(str(p.tag.number))
        if "Fixd" in primary.tag and pos_str in ("NOUN", "ADJF"):
            num_code = NUMBER_CODES["both"]
        elif len(nums_seen) >= 2:
            num_code = NUMBER_CODES["both"]
        elif len(nums_seen) == 1:
            num_code = NUMBER_CODES[next(iter(nums_seen))]
        else:
            num_code = NUMBER_CODES.get(str(primary.tag.number or ""), 0)

        cases6 = 0
        flags = 0
        if "anim" in primary.tag:
            flags |= FLAG_ANIM
        elif "inan" in primary.tag:
            flags |= FLAG_INAN
        if "Pltm" in primary.tag or lm in STRICT_PLTM_LEMMAS:
            flags |= FLAG_PLTM
        if "Sgtm" in primary.tag:
            flags |= FLAG_SGTM
        if "Fixd" in primary.tag:
            flags |= FLAG_FIXD

        # Aggregate POS/case flags & homonym candidates across all known dictionary parses
        cand_best: dict[tuple[str, str], tuple[float, int, bool]] = {}
        has_standard = any(
            not any(g in p.tag for g in ("Arch", "Infr", "Slng", "Dist", "Erro"))
            for p in known
        )

        for p in known:
            p_pos = str(p.tag.POS or "")
            if p_pos == "NOUN":
                flags |= FLAG_HAS_NOUN
                if "nomn" in p.tag or "Fixd" in p.tag:
                    flags |= FLAG_HAS_NOMN_NOUN
            elif p_pos in ("ADJF", "PRTF", "ADJS"):
                flags |= FLAG_HAS_ADJ
            elif p_pos in ("VERB", "INFN", "GRND", "PRTS"):
                flags |= FLAG_HAS_VERB
            if any(g in p.tag for g in ("Name", "Surn", "Patr", "Geox")):
                flags |= FLAG_HAS_PROPN

            c6 = 0
            if p_pos in GOVERNED_POS_SET:
                flags |= FLAG_HAS_GOVERNED
                if "Fixd" in p.tag:
                    c6 = CASE_MASK_ALL
                elif p.tag.case:
                    c6 = CASE_TO_6BIT.get(str(p.tag.case), 0)
                cases6 |= c6

            is_noisy = any(g in p.tag for g in ("Arch", "Infr", "Slng", "Dist", "Erro"))
            if has_standard and is_noisy and p.score < 0.05:
                continue
            if not p_pos:
                continue
            c_lem = _parse_cand_lemma(p)
            key = (c_lem, p_pos)
            prev = cand_best.get(key)
            if prev is None:
                cand_best[key] = (float(p.score), c6, is_noisy)
            else:
                cand_best[key] = (max(prev[0], float(p.score)), prev[1] | c6, prev[2] and is_noisy)

        bits = pos_code | (gen_code << 5) | (num_code << 7) | ((cases6 & CASE_MASK_ALL) << CASE_BIT_OFFSET) | flags
        morph_map[w] = bits

        # Ensure primary (lm, pos_str) is represented in candidates
        prim_key = (lm, pos_str) if pos_str else None
        if prim_key and prim_key not in cand_best:
            cand_best[prim_key] = (1.0, cases6, False)

        # Keep only genuinely homonymous forms (>= 2 distinct (lemma, POS) parses)
        if len(cand_best) >= 2:
            # Sort: primary first, then by score descending
            items = sorted(
                cand_best.items(),
                key=lambda kv: (kv[0] != prim_key, -kv[1][0], kv[0][0], kv[0][1]),
            )
            kept = []
            for idx_c, ((c_lem, c_pos), (sc, c6, _)) in enumerate(items):
                if idx_c == 0 or sc >= 0.005 or c_pos == "NOUN":
                    kept.append(f"{c_lem}:{c_pos}:{c6:x}")
                if len(kept) >= 5:
                    break
            if len(kept) >= 2:
                alt_map[w] = "|".join(kept)

    return morph_map, alt_map


def _inflect_form(parse, grams: set[str]) -> str:
    try:
        inf = parse.inflect(grams)
    except Exception:
        return ""
    if inf and inf.word:
        return norm_yo(inf.word.lower())
    return ""


def build_paradigms(
    clean_lemmas: dict[str, str],
    clean_types: dict[str, tuple[str, str]],
    morph: pymorphy3.MorphAnalyzer,
) -> tuple[list[tuple[int, str, int, str]], list[tuple[str, int, int]]]:
    """Build compact deduplicated suffix inflection paradigms for NOUN and ADJF/PRTF lemmas."""
    cand_lemmas: set[str] = set()
    for lm in clean_lemmas.values():
        if CYR_WORD_RE.fullmatch(lm):
            cand_lemmas.add(lm)
    for t in clean_types:
        if CYR_WORD_RE.fullmatch(t):
            cand_lemmas.add(norm_yo(t))

    paradigm_to_id: dict[tuple[str, int, str], int] = {}
    paradigms_list: list[tuple[int, str, int, str]] = []
    words_list: list[tuple[str, int, int]] = []

    def intern_paradigm(kind: str, strip: int, endings_csv: str) -> int:
        key = (kind, strip, endings_csv)
        pid = paradigm_to_id.get(key)
        if pid is None:
            pid = len(paradigms_list) + 1  # pid 0 is reserved for Fixd / indeclinable
            paradigm_to_id[key] = pid
            paradigms_list.append((pid, kind, strip, endings_csv))
        return pid

    oy_rev = {v: k for k, v in PREFER_OY_ADJS.items()}

    for lm in sorted(cand_lemmas):
        parses = morph.parse(lm)
        known = [p for p in parses if is_in_opencorpora(p)]
        if not known and lm in oy_rev:
            known = [p for p in morph.parse(oy_rev[lm]) if is_in_opencorpora(p)]
        if not known:
            # Handle indeclinable known types like мфу
            tinfo = clean_types.get(lm)
            if tinfo is not None and tinfo[1] and lm in ("мфу",):
                g_code = GENDER_CODES.get(tinfo[0], 0)
                wflags = PF_NOUN | (g_code << 2) | PF_INAN | PF_FIXD
                words_list.append((lm, 0, wflags))
            continue

        # Pick best NOUN or ADJF/PRTF canonical parse matching lm
        noun_p = None
        noun_propn = None
        adj_p = None
        for p in known:
            p_pos = str(p.tag.POS or "")
            p_lem = _parse_cand_lemma(p)
            if p_pos == "NOUN" and (p_lem == lm or (lm in STRICT_PLTM_LEMMAS and norm_yo(p.normal_form) == lm)):
                is_propn = any(g in p.tag for g in ("Name", "Surn", "Patr", "Geox"))
                if not is_propn and noun_p is None:
                    noun_p = p
                elif is_propn and noun_propn is None:
                    noun_propn = p
            elif p_pos in ("ADJF", "PRTF") and (p_lem == lm or (lm in oy_rev and p_lem == oy_rev[lm])):
                if adj_p is None:
                    adj_p = p

        # Prefer common NOUN over proper NOUN; if top known parse is ADJF/PRTF and no common noun exists (or ADJF dominates), use ADJ
        chosen = None
        kind = ""
        if noun_p is not None and adj_p is not None:
            # e.g. "ванная" / "мороженое" (substantivized noun) vs "зеленый"
            if known[0].tag.POS in ("ADJF", "PRTF") and known[0].score > noun_p.score * 1.2:
                chosen, kind = adj_p, "A"
            else:
                chosen, kind = noun_p, "N"
        elif noun_p is not None:
            chosen, kind = noun_p, "N"
        elif adj_p is not None:
            chosen, kind = adj_p, "A"
        elif noun_propn is not None:
            chosen, kind = noun_propn, "N"
        else:
            continue

        if kind == "N":
            g_code = GENDER_CODES.get(str(chosen.tag.gender or ""), 0)
            wflags = PF_NOUN | (g_code << 2)
            if "anim" in chosen.tag:
                wflags |= PF_ANIM
            else:
                wflags |= PF_INAN
            is_pltm = ("Pltm" in chosen.tag) or (lm in STRICT_PLTM_LEMMAS)
            is_sgtm = ("Sgtm" in chosen.tag) and not is_pltm
            is_fixd = "Fixd" in chosen.tag
            if is_pltm:
                wflags |= PF_PLTM
            if is_sgtm:
                wflags |= PF_SGTM
            if is_fixd:
                wflags |= PF_FIXD
                words_list.append((lm, 0, wflags))
                continue

            forms: list[str] = []
            # 0..5: sing
            for c in CASE_ORDER:
                forms.append("" if is_pltm else _inflect_form(chosen, {"sing", c}))
            # 6..11: plur
            for c in CASE_ORDER:
                forms.append("" if is_sgtm else _inflect_form(chosen, {"plur", c}))

            non_empty = [f for f in forms if f]
            if not non_empty:
                continue
            lcp = _lcp_len([lm] + non_empty)
            strip = len(lm) - lcp
            endings = [f[lcp:] if f else "-" for f in forms]
            pid = intern_paradigm("N", strip, ",".join(endings))
            words_list.append((lm, pid, wflags))
        else:
            # ADJF / PRTF: 26 slots
            # 0..5: masc,sing (nomn,gent,datv,accs_inan,ablt,loct)
            # 6..11: femn,sing
            # 12..17: neut,sing
            # 18..23: plur (accs_inan)
            # 24: masc,sing,accs,anim
            # 25: plur,accs,anim
            wflags = PF_ADJ
            if "Fixd" in chosen.tag:
                wflags |= PF_FIXD
                words_list.append((lm, 0, wflags))
                continue

            forms = []
            for gen in ("masc", "femn", "neut"):
                for c in CASE_ORDER:
                    if gen == "masc" and c == "accs":
                        f = _inflect_form(chosen, {"masc", "sing", "accs", "inan"}) or _inflect_form(chosen, {"masc", "sing", "accs"})
                    else:
                        f = _inflect_form(chosen, {gen, "sing", c})
                    forms.append(f)
            for c in CASE_ORDER:
                if c == "accs":
                    f = _inflect_form(chosen, {"plur", "accs", "inan"}) or _inflect_form(chosen, {"plur", "accs"})
                else:
                    f = _inflect_form(chosen, {"plur", c})
                forms.append(f)
            # Slot 24: masc,sing,accs,anim; Slot 25: plur,accs,anim
            forms.append(_inflect_form(chosen, {"masc", "sing", "accs", "anim"}) or forms[1])
            forms.append(_inflect_form(chosen, {"plur", "accs", "anim"}) or forms[19])

            if lm in oy_rev:
                forms[0] = lm
                if forms[3] == oy_rev[lm]:
                    forms[3] = lm

            non_empty = [f for f in forms if f]
            if not non_empty:
                continue
            lcp = _lcp_len([lm] + non_empty)
            strip = len(lm) - lcp
            endings = [f[lcp:] if f else "-" for f in forms]
            pid = intern_paradigm("A", strip, ",".join(endings))
            words_list.append((lm, pid, wflags))

    return paradigms_list, words_list


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--extra-words-tsv", type=Path, default=None, help="Optional TSV (word\\tdemand) to expand vocab coverage")
    parser.add_argument("--max-extra", type=int, default=120000)
    args = parser.parse_args()

    t0 = time.monotonic()
    morph = pymorphy3.MorphAnalyzer()

    raw_lemmas: dict[str, str] = json.loads((RAW_DIR / "lemmas.json").read_text(encoding="utf-8"))
    raw_adj: dict[str, str] = json.loads((RAW_DIR / "adj_forms.json").read_text(encoding="utf-8"))
    raw_types: dict[str, dict] = json.loads((RAW_DIR / "types.json").read_text(encoding="utf-8"))
    overrides = load_overrides(DATA_DIR / "overrides.tsv")

    print(f"[build] Loaded raw: lemmas={len(raw_lemmas):,}, adj_forms={len(raw_adj):,}, types={len(raw_types):,}, overrides={len(overrides):,}", flush=True)

    # Normalize adj_forms keys in two passes:
    # Pass 1: exact ё-free keys win; Pass 2: ё-variants & legacy "|plur|plur" fill missing slots only.
    # Also drop legacy verb-infinitive keys (e.g. "увлажнять|masc|sing" -> "увлажняющий") so Rule 4 (PRTF)
    # indexes them cleanly under their adjectival lemma ("увлажняющий|...").
    clean_adj: dict[str, str] = {}
    adj_form_to_lemmas: dict[str, set[str]] = defaultdict(set)
    parsed_adj: list[tuple[bool, str, str, str]] = []
    for k, form in raw_adj.items():
        if not k or not form:
            continue
        parts = k.split("|")
        if len(parts) != 3:
            continue
        orig_base = parts[0].lower()
        fl = form.lower()
        if orig_base.endswith(("ть", "ться")) and fl != orig_base:
            continue
        al = norm_yo(orig_base)
        gender, number = parts[1], parts[2]
        if number == "plur":
            gender = ""
        norm_key = f"{al}|{gender}|{number}"
        is_exact = (orig_base == al) and (parts[1] == gender)
        parsed_adj.append((is_exact, norm_key, al, fl))

    for is_exact, norm_key, al, fl in sorted(parsed_adj, key=lambda x: (not x[0], x[1])):
        if is_exact:
            clean_adj[norm_key] = fl
        else:
            clean_adj.setdefault(norm_key, fl)
        adj_form_to_lemmas[fl].add(al)
        adj_form_to_lemmas[norm_yo(fl)].add(al)

    # Normalize stressed -ой adjectives in adj_forms (беспроводный -> беспроводной, складный -> складной, etc.)
    for old_base, new_base in PREFER_OY_ADJS.items():
        for suffix in ("masc|sing", "femn|sing", "neut|sing", "|plur"):
            old_k = f"{old_base}|{suffix}"
            new_k = f"{new_base}|{suffix}"
            form_val = clean_adj.pop(old_k, None)
            if suffix == "masc|sing":
                clean_adj[new_k] = new_base
                adj_form_to_lemmas[new_base].add(new_base)
            elif form_val is not None:
                clean_adj[new_k] = form_val
                adj_form_to_lemmas[form_val].add(new_base)
                adj_form_to_lemmas[norm_yo(form_val)].add(new_base)
        for fl_key, s in list(adj_form_to_lemmas.items()):
            if old_base in s:
                s.discard(old_base)
                s.add(new_base)

    for k, v in REQUIRED_ADJ_FORMS.items():
        clean_adj.setdefault(k, v)

    # Collect target vocabulary: all Cyrillic words from raw_lemmas + optional extra public Russian words
    vocab: set[str] = set()
    skipped_non_cyr = 0
    for w in raw_lemmas:
        wl = w.strip().lower()
        if CYR_WORD_RE.fullmatch(wl):
            vocab.add(wl)
        else:
            skipped_non_cyr += 1

    extra_added = 0
    if args.extra_words_tsv and args.extra_words_tsv.exists():
        for idx, line in enumerate(args.extra_words_tsv.read_text(encoding="utf-8").splitlines()):
            if idx >= args.max_extra:
                break
            w = line.split("\t")[0].strip().lower()
            if CYR_WORD_RE.fullmatch(w) and w not in vocab:
                vocab.add(w)
                extra_added += 1

    # Always include core Russian base lemmas & override keys (including 1-letter & unit abbreviations)
    for w in CORE_EXTRA_WORDS:
        if CYR_WORD_RE.fullmatch(w):
            vocab.add(w)
    for w in overrides:
        if CYR_OVERRIDE_RE.fullmatch(w):
            vocab.add(w)

    print(f"[build] Clean Cyrillic vocab={len(vocab):,} (extra_added={extra_added:,}, skipped_non_cyr={skipped_non_cyr:,})", flush=True)

    stats: Counter = Counter()
    changed_samples: list[tuple[str, str, str]] = []
    clean_lemmas: dict[str, str] = {}
    extra_adj_forms: dict[str, str] = {}

    for i, w in enumerate(sorted(vocab), start=1):
        if i % 100000 == 0:
            print(f"[build] Processed {i:,}/{len(vocab):,} words ({time.monotonic()-t0:.1f}s)...", flush=True)
        if w in overrides:
            new_l = overrides[w]
            stats["override"] += 1
        else:
            new_l = smart_lemmatize(w, morph, adj_form_to_lemmas, extra_adj_forms, stats)
            if new_l in PREFER_OY_ADJS:
                new_l = PREFER_OY_ADJS[new_l]
                stats["fix_prefer_oy_adj"] += 1
        clean_lemmas[w] = new_l
        wn = norm_yo(w)
        if wn != w and wn not in clean_lemmas:
            clean_lemmas[wn] = overrides.get(wn, new_l)

        old_l = raw_lemmas.get(w)
        if old_l is not None and norm_yo(old_l.lower()) != new_l and len(changed_samples) < 200:
            changed_samples.append((w, old_l, new_l))

    # Add protected Russian compound toponym forms (multi-hyphen cities like ростов-на-дону)
    for w, l in COMPOUND_TOPONYM_FORMS.items():
        clean_lemmas[w] = l
        stats["compound_toponym"] += 1

    # v0.3.1: Repair OOV artifacts that poison the dictionary-verified suffix fallback.
    protected = set(overrides) | set(CORE_EXTRA_WORDS) | set(COMPOUND_TOPONYM_FORMS) | set(clean_types_keys(raw_types))
    known_cache: dict[str, bool] = {}

    def oc_known(word: str) -> bool:
        hit = known_cache.get(word)
        if hit is None:
            hit = any(is_in_opencorpora(p) for p in morph.parse(word))
            known_cache[word] = hit
        return hit

    # (a) OOV masculine genitive forms stored as their own lemma (картхолдера -> картхолдер, ледобура -> ледобур):
    #     word is unknown to OpenCorpora, ends with -а, base is a canonical masc nominative non-PropN noun,
    #     and pymorphy recognizes gent-of-base among its parses.
    for w in sorted(clean_lemmas):
        if w in protected or clean_lemmas[w] != w or len(w) < 5 or not w.endswith("а") or "-" in w:
            continue
        base = w[:-1]
        if base[-1] not in CONSONANTS or clean_lemmas.get(base) != base or oc_known(w):
            continue
        pb = morph.parse(base)[0]
        if (
            norm_yo(pb.normal_form) == base
            and pb.tag.POS == "NOUN"
            and "masc" in pb.tag
            and "nomn" in pb.tag
            and not ("Surn" in pb.tag and base.endswith(("ов", "ев", "ин", "ын")))
            and not any(g in pb.tag for g in ("Name", "Patr"))
            and any(norm_yo(p.normal_form) == base and "gent" in p.tag for p in morph.parse(w))
        ):
            clean_lemmas[w] = base
            stats["fix_oov_masc_gent_self_lemma"] += 1

    # (b) Truncated OOV fragments (кресл, метл, академи, алюмини): the form is unknown to OpenCorpora, but appending
    #     a single vowel/soft sign yields a real dictionary word. Such fragments are dropped instead of being trusted
    #     as canonical lemmas (кресл) or mapped to invented lemmas (метл -> метло).
    fragments: list[str] = []
    for w in sorted(clean_lemmas):
        if w in protected or len(w) < 3 or "-" in w or oc_known(w):
            continue
        lm = clean_lemmas[w]
        if lm != w and oc_known(lm):
            continue
        if any((w + v) in clean_lemmas and oc_known(w + v) for v in ("о", "а", "я", "ы", "и", "ь", "е")):
            fragments.append(w)
    for w in fragments:
        del clean_lemmas[w]
    stats["drop_oov_fragment"] = len(fragments)

    # (c) Ensure every known canonical target lemma (e.g. выдача from выдачи->выдача) is self-present in clean_lemmas
    #     so dictionary-verified suffix fallback & paradigm inference can match its unseen oblique forms (выдачей->выдача).
    added_canon = 0
    for target in sorted(set(clean_lemmas.values())):
        if target not in clean_lemmas and CYR_WORD_RE.fullmatch(target) and oc_known(target):
            clean_lemmas[target] = target
            added_canon += 1
    stats["ensure_canonical_target_lemma"] = added_canon

    # Re-apply overrides last so ё-variants or toponyms can never overwrite an explicit override
    for w, l in overrides.items():
        if CYR_OVERRIDE_RE.fullmatch(w):
            clean_lemmas[w] = l

    # Merge auto-derived PRTF agreement paradigms without overwriting curated adj_forms
    prtf_adj_added = 0
    for k, v in extra_adj_forms.items():
        if k not in clean_adj:
            clean_adj[k] = v
            prtf_adj_added += 1

    # Merge required product types (fills missing or empty gender/number entries like кофемашина, мфу)
    clean_types: dict[str, tuple[str, str]] = {
        k.lower(): (str(v.get("gender") or ""), str(v.get("number") or ""))
        for k, v in raw_types.items()
        if k
    }
    for t_name, (t_gen, t_num) in REQUIRED_TYPES.items():
        cur = clean_types.get(t_name)
        if cur is None or not cur[1]:
            clean_types[t_name] = (t_gen, t_num)

    # Build v0.3 compact morphology, homonym candidates, and inflection paradigms
    print("[build] Building v0.3 morph.tsv.gz & alt_lemmas.tsv.gz...", flush=True)
    morph_map, alt_map = build_morph_and_alt_lemmas(clean_lemmas, clean_types, morph)
    print(f"[build] morph={len(morph_map):,}, alt_lemmas={len(alt_map):,} ({time.monotonic()-t0:.1f}s)", flush=True)

    print("[build] Building v0.3 paradigms.tsv.gz...", flush=True)
    paradigms_list, paradigm_words = build_paradigms(clean_lemmas, clean_types, morph)
    print(f"[build] paradigms={len(paradigms_list):,}, inflectable_lemmas={len(paradigm_words):,} ({time.monotonic()-t0:.1f}s)", flush=True)

    # Save compact .tsv.gz files
    DATA_DIR.mkdir(parents=True, exist_ok=True)
    write_tsv_gz(DATA_DIR / "lemmas.tsv.gz", sorted((w, l) for w, l in clean_lemmas.items()))
    write_tsv_gz(DATA_DIR / "adj_forms.tsv.gz", sorted((k, v) for k, v in clean_adj.items()))
    write_tsv_gz(
        DATA_DIR / "types.tsv.gz",
        sorted((k, g, n) for k, (g, n) in clean_types.items()),
    )
    write_tsv_gz(
        DATA_DIR / "morph.tsv.gz",
        sorted((w, f"{bits:x}") for w, bits in morph_map.items()),
    )
    write_tsv_gz(
        DATA_DIR / "alt_lemmas.tsv.gz",
        sorted((w, val) for w, val in alt_map.items()),
    )
    paradigm_rows: list[tuple[str, ...]] = [
        ("P", str(pid), kind, str(strip), endings) for pid, kind, strip, endings in paradigms_list
    ]
    paradigm_rows.extend(
        ("W", lm, str(pid), f"{wflags:x}") for lm, pid, wflags in paradigm_words
    )
    write_tsv_gz(DATA_DIR / "paradigms.tsv.gz", paradigm_rows)

    report = {
        "total_lemmas": len(clean_lemmas),
        "total_adj_forms": len(clean_adj),
        "prtf_adj_forms_added": prtf_adj_added,
        "total_types": len(clean_types),
        "total_morph": len(morph_map),
        "total_alt_lemmas": len(alt_map),
        "total_paradigms": len(paradigms_list),
        "total_paradigm_words": len(paradigm_words),
        "overrides_applied": len(overrides),
        "extra_words_added": extra_added,
        "rule_stats": dict(stats),
        "sizes_kb": {
            "lemmas.tsv.gz": round((DATA_DIR / "lemmas.tsv.gz").stat().st_size / 1024, 1),
            "adj_forms.tsv.gz": round((DATA_DIR / "adj_forms.tsv.gz").stat().st_size / 1024, 1),
            "types.tsv.gz": round((DATA_DIR / "types.tsv.gz").stat().st_size / 1024, 1),
            "morph.tsv.gz": round((DATA_DIR / "morph.tsv.gz").stat().st_size / 1024, 1),
            "alt_lemmas.tsv.gz": round((DATA_DIR / "alt_lemmas.tsv.gz").stat().st_size / 1024, 1),
            "paradigms.tsv.gz": round((DATA_DIR / "paradigms.tsv.gz").stat().st_size / 1024, 1),
        },
        "changed_vs_raw_sample": changed_samples[:60],
        "seconds": round(time.monotonic() - t0, 1),
    }
    (DATA_DIR / "build_report.json").write_text(json.dumps(report, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    print("[build] Done!", json.dumps({k: v for k, v in report.items() if k != "changed_vs_raw_sample"}, ensure_ascii=False, indent=2))


if __name__ == "__main__":
    main()
