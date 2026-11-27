"""rulemma: быстрый лемматизатор русского языка и e-commerce запросов на Go для Python."""
from .client import (
    DEFAULT_STOP_WORDS,
    ECOMMERCE_STOP_WORDS,
    GENERAL_STOP_WORDS,
    RuLemma,
    normalize_homoglyphs,
    normalize_phrase,
    pluralize_form,
    preposition,
)

__all__ = [
    "RuLemma",
    "DEFAULT_STOP_WORDS",
    "GENERAL_STOP_WORDS",
    "ECOMMERCE_STOP_WORDS",
    "normalize_phrase",
    "normalize_homoglyphs",
    "pluralize_form",
    "preposition",
    "__version__",
]
__version__ = "0.4.0"
__authors__ = ("Коршиков Дмитрий", "Gemini")
