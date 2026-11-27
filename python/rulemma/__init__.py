"""rulemma: быстрый лемматизатор русского языка и e-commerce запросов на Go для Python."""
from .client import DEFAULT_STOP_WORDS, RuLemma, normalize_phrase

__all__ = ["RuLemma", "DEFAULT_STOP_WORDS", "normalize_phrase", "__version__"]
__version__ = "0.2.2"
__authors__ = ("Коршиков Дмитрий", "Gemini")
