"""11. Согласование прилагательных и причастий по роду/числу и сборка SEO-названий товаров."""
from __future__ import annotations

from rulemma import RuLemma


def main() -> None:
    catalog = [
        ("Сумка кожаная", "Сумка", ["кожаный", "синие", "женские"], 100),
        ("Ведро строительное", "Ведро", ["оцинкованные", "большие", "10 л"], 100),
        ("Игрушка деревянная", "Игрушка", ["развивающие", "детские", "яркий"], 100),
        ("Ножницы садовые", "Ножницы", ["оксидированный", "прочная"], 100),
        ("Холодильник двухкамерный", "Холодильник", ["встраиваемые", "белая"], 100),
    ]

    with RuLemma() as lem:
        for base_name, ptype, kws, max_len in catalog:
            seo, added, agreed = lem.build_seo_name(base_name, ptype, kws, max_len)
            assert added > 0 and len(seo) <= max_len
            print(f"{ptype:14s} | {seo}  (+{added}, согласовано={agreed})")

        seo_bucket, _, _ = lem.build_seo_name("Ведро строительное", "Ведро", ["оцинкованные", "большие"], 100)
        assert seo_bucket == "Ведро строительное, оцинкованное, большое", seo_bucket


if __name__ == "__main__":
    main()
