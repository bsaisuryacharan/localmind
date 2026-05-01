"""Tests for the skill loader + validator."""

from __future__ import annotations

from pathlib import Path

import pytest

from localmind_orchestrator.skills import (
    Skill,
    load_all_skills,
    load_skill,
    validate_skill,
)

# Resolve the bundled-skills dir relative to this test file:
# orchestrator/localmind_orchestrator/tests/test_skills.py -> orchestrator/skills
SKILL_DIR = (
    Path(__file__).resolve().parent.parent.parent / "skills"
)


EXPECTED_SKILLS = {
    "orchestrator",
    "researcher",
    "coder",
    "reviewer",
    "synthesizer",
}


@pytest.mark.parametrize("name", sorted(EXPECTED_SKILLS))
def test_load_each_starter_skill(name: str) -> None:
    """Every starter skill parses, has frontmatter, and a non-empty body."""
    skill = load_skill(SKILL_DIR, name)
    assert skill.name == name
    assert skill.description.strip(), f"{name}: empty description"
    assert skill.recommended_model.strip(), f"{name}: empty recommended_model"
    assert skill.system_prompt.strip(), f"{name}: empty system prompt"
    assert skill.cloud is False, f"{name}: cloud must be False in v0.3.0"
    assert skill.source_path.endswith(f"{name}.md")


def test_load_all_returns_exactly_five_skills() -> None:
    skills = load_all_skills(SKILL_DIR)
    assert set(skills.keys()) == EXPECTED_SKILLS
    assert len(skills) == 5


def test_validate_rejects_cloud_true() -> None:
    """v0.3.0 lock: cloud=true must raise."""
    skill = Skill(
        name="bad",
        description="x",
        recommended_model="qwen2.5:3b",
        cloud=True,
        tools=[],
        max_tool_calls=1,
        system_prompt="body",
        source_path="/tmp/bad.md",
    )
    with pytest.raises(ValueError, match="cloud=true"):
        validate_skill(skill)


def test_validate_rejects_empty_recommended_model() -> None:
    skill = Skill(
        name="bad",
        description="x",
        recommended_model=" ",
        system_prompt="body",
        source_path="/tmp/bad.md",
    )
    with pytest.raises(ValueError, match="recommended_model"):
        validate_skill(skill)


def test_validate_rejects_empty_system_prompt() -> None:
    skill = Skill(
        name="bad",
        description="x",
        recommended_model="qwen2.5:3b",
        system_prompt="   ",
        source_path="/tmp/bad.md",
    )
    with pytest.raises(ValueError, match="system prompt"):
        validate_skill(skill)


def test_validate_rejects_negative_max_tool_calls() -> None:
    skill = Skill(
        name="bad",
        description="x",
        recommended_model="qwen2.5:3b",
        max_tool_calls=-1,
        system_prompt="body",
        source_path="/tmp/bad.md",
    )
    with pytest.raises(ValueError, match="max_tool_calls"):
        validate_skill(skill)


def test_load_missing_skill_raises_filenotfound(tmp_path: Path) -> None:
    with pytest.raises(FileNotFoundError):
        load_skill(tmp_path, "nope")


def test_orchestrator_has_no_tools() -> None:
    """Orchestrator must not be allowed to call tools directly."""
    skill = load_skill(SKILL_DIR, "orchestrator")
    assert skill.tools == []


def test_synthesizer_has_no_tools() -> None:
    skill = load_skill(SKILL_DIR, "synthesizer")
    assert skill.tools == []


def test_researcher_tool_whitelist() -> None:
    skill = load_skill(SKILL_DIR, "researcher")
    assert set(skill.tools) == {"search_files", "list_files", "read_file"}


def test_coder_can_only_read() -> None:
    """v0.3.0: coder is read-only."""
    skill = load_skill(SKILL_DIR, "coder")
    assert skill.tools == ["read_file"]
