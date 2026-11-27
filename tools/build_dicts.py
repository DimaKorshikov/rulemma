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

    # Always include all override keys (including 1-letter designations & unit abbreviations)
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
        clean_lemmas[w] = new_l
        wn = norm_yo(w)
        if wn != w and wn not in clean_lemmas:
            clean_lemmas[wn] = overrides.get(wn, new_l)

        old_l = raw_lemmas.get(w)
        if old_l is not None and norm_yo(old_l.lower()) != new_l and len(changed_samples) < 200:
            changed_samples.append((w, old_l, new_l))

    # Re-apply overrides last so ё-variants can never overwrite an override
    for w, l in overrides.items():
        if CYR_OVERRIDE_RE.fullmatch(w):
            clean_lemmas[w] = l

    # Merge auto-derived PRTF agreement paradigms without overwriting curated adj_forms
    prtf_adj_added = 0
    for k, v in extra_adj_forms.items():
        if k not in clean_adj:
            clean_adj[k] = v
            prtf_adj_added += 1

    # Save compact .tsv.gz files
    DATA_DIR.mkdir(parents=True, exist_ok=True)
    write_tsv_gz(DATA_DIR / "lemmas.tsv.gz", sorted((w, l) for w, l in clean_lemmas.items()))
    write_tsv_gz(DATA_DIR / "adj_forms.tsv.gz", sorted((k, v) for k, v in clean_adj.items()))
    write_tsv_gz(
        DATA_DIR / "types.tsv.gz",
        sorted((k.lower(), str(v.get("gender") or ""), str(v.get("number") or "")) for k, v in raw_types.items() if k),
    )

    report = {
        "total_lemmas": len(clean_lemmas),
        "total_adj_forms": len(clean_adj),
        "prtf_adj_forms_added": prtf_adj_added,
        "total_types": len(raw_types),
        "overrides_applied": len(overrides),
        "extra_words_added": extra_added,
        "rule_stats": dict(stats),
        "sizes_kb": {
            "lemmas.tsv.gz": round((DATA_DIR / "lemmas.tsv.gz").stat().st_size / 1024, 1),
            "adj_forms.tsv.gz": round((DATA_DIR / "adj_forms.tsv.gz").stat().st_size / 1024, 1),
            "types.tsv.gz": round((DATA_DIR / "types.tsv.gz").stat().st_size / 1024, 1),
        },
        "changed_vs_raw_sample": changed_samples[:60],
        "seconds": round(time.monotonic() - t0, 1),
    }
    (DATA_DIR / "build_report.json").write_text(json.dumps(report, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    print("[build] Done!", json.dumps({k: v for k, v in report.items() if k != "changed_vs_raw_sample"}, ensure_ascii=False, indent=2))


if __name__ == "__main__":
    main()
