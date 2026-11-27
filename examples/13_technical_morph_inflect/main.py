"""Example 13: rulemma v0.3 — technical profile, compact morphology & Russian inflection."""
from __future__ import annotations

from rulemma import RuLemma, normalize_homoglyphs, pluralize_form, preposition


def main() -> None:
    print("Homoglyphs:", normalize_homoglyphs("Кабель 5cм intеl рrо"))

    with RuLemma(min_version="0.3.0") as lem:
        # 1. Opt-in technical profile: decimals, dimensions, б/у, m.2, 18+, 12 В vs 3 в 1
        assert lem.semantic_key("бензопила 2,6 л.с.", profile="technical") != lem.semantic_key(
            "бензопила 6,2 л.с.", profile="technical"
        )
        assert lem.semantic_key("термос 0,5 л", profile="technical") == lem.semantic_key(
            "термос 0.5 л", profile="technical"
        )
        assert lem.semantic_key("плитка 100×100 см", profile="technical") != lem.semantic_key(
            "плитка 100 см", profile="technical"
        )
        assert lem.semantic_key("винт 5 мм", profile="technical") == lem.semantic_key(
            "винт 5мм", profile="technical"
        )
        print("Tech key (б/у):", lem.semantic_key("зимние ботинки б/у", profile="technical"))

        # Preposition bindings & keep_words
        k1 = lem.key("кофе без сахара с молоком", preserve_prep_bindings=True)
        k2 = lem.key("кофе с сахаром без молока", preserve_prep_bindings=True)
        assert k1 != k2
        print("Bound negation keys:", k1, "!=", k2)

        p1 = lem.key("пластиковые окна", bind_purpose_prep=True)
        p2 = lem.key("пластик для окон", bind_purpose_prep=True)
        assert p1 != p2
        print("Bound purpose keys:", p1, "!=", p2)

        # 2. Compact morphology (Analyze / WordCandidates / PrepGoverns)
        pil = lem.analyze("пил")
        print("Analyze('пил'):", pil["lemma"], pil["pos"], [(c["lemma"], c["pos"]) for c in pil["candidates"]])
        assert lem.prep_governs("с", "цинком") is True
        assert lem.prep_governs("с", "1000") is False
        assert lem.prep_governs("в", "д") is False

        # 3. Inflection, phrase inflection, number agreement & phonetic prepositions
        phrase_accs, ok = lem.inflect_phrase("стиральная машина LG F2V5", "accs")
        assert ok and phrase_accs == "стиральную машину LG F2V5"
        region_loct, ok = lem.inflect_phrase("Краснодарский край", "loct")
        assert ok and region_loct == "Краснодарском крае"
        print("InflectPhrase:", phrase_accs, "|", preposition("в", region_loct), region_loct)

        agreed = [lem.agree_number(n, "товар") for n in (1, 2, 5, 21, 1.5)]
        print("AgreeNumber:", ", ".join(agreed))
        assert pluralize_form(21, "товар", "товара", "товаров") == "товар"


if __name__ == "__main__":
    main()
