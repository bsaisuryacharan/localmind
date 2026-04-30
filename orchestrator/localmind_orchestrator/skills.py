"""Skill loading + validation for the localmind orchestrator.

A skill is a markdown file with YAML frontmatter declaring metadata
(name, description, recommended model, tool whitelist, etc.) followed
by the system prompt body. Skills are how each agent persona is
configured -- they're the "what kind of worker is this" file.

In v0.3.0 every skill is locked to local-only execution. The
``cloud`` frontmatter key is reserved but must be ``false``;
attempting to load a skill with ``cloud: true`` raises ``ValueError``
from :func:`validate_skill`. The cloud opt-in lands in v0.3.4.
"""

from __future__ import annotations

from pathlib import Path

import frontmatter
from pydantic import BaseModel, Field


class Skill(BaseModel):
    """Parsed representation of a skill markdown bundle."""

    name: str
    description: str
    recommended_model: str
    cloud: bool = False
    """If True, this skill is permitted to call cloud APIs.

    Locked to ``False`` in v0.3.0. ``validate_skill`` raises if True.
    """
    tools: list[str] = Field(default_factory=list)
    """MCP tool names this skill is allowed to call.

    Default-deny: a tool not listed here cannot be invoked by agents
    running this skill (enforced in :mod:`localmind_orchestrator.tools`).
    """
    max_tool_calls: int = 5
    system_prompt: str
    source_path: str


def _parse(path: Path) -> Skill:
    """Parse a skill markdown file into a Skill object."""
    post = frontmatter.load(str(path))
    meta = dict(post.metadata)
    body = (post.content or "").strip()

    # Pull required + optional fields, defaulting where the model allows.
    name = meta.get("name")
    description = meta.get("description")
    recommended_model = meta.get("recommended_model")
    if not name:
        raise ValueError(f"skill {path}: frontmatter missing 'name'")
    if not description:
        raise ValueError(f"skill {path}: frontmatter missing 'description'")
    if not recommended_model:
        raise ValueError(
            f"skill {path}: frontmatter missing 'recommended_model'"
        )

    skill = Skill(
        name=str(name),
        description=str(description),
        recommended_model=str(recommended_model),
        cloud=bool(meta.get("cloud", False)),
        tools=list(meta.get("tools") or []),
        max_tool_calls=int(meta.get("max_tool_calls", 5)),
        system_prompt=body,
        source_path=str(path.resolve()),
    )
    return skill


def load_skill(skill_dir: Path, name: str) -> Skill:
    """Load skill ``<skill_dir>/<name>.md``.

    Raises:
        FileNotFoundError: if the skill file does not exist.
        ValueError: if the skill is malformed (see :func:`validate_skill`).
    """
    path = Path(skill_dir) / f"{name}.md"
    if not path.is_file():
        raise FileNotFoundError(f"skill not found: {path}")
    skill = _parse(path)
    validate_skill(skill)
    return skill


def load_all_skills(skill_dir: Path) -> dict[str, Skill]:
    """Load every ``*.md`` file under ``skill_dir`` as a Skill.

    Returns:
        Mapping of skill name to Skill, keyed by the ``name`` field of
        the frontmatter (not the filename, though they should match).
    """
    skill_dir = Path(skill_dir)
    if not skill_dir.is_dir():
        raise FileNotFoundError(f"skill dir not found: {skill_dir}")

    out: dict[str, Skill] = {}
    for path in sorted(skill_dir.glob("*.md")):
        skill = _parse(path)
        validate_skill(skill)
        if skill.name in out:
            raise ValueError(
                f"duplicate skill name {skill.name!r} "
                f"({out[skill.name].source_path} and {skill.source_path})"
            )
        out[skill.name] = skill
    return out


def validate_skill(skill: Skill) -> None:
    """Raise ``ValueError`` if the skill's frontmatter is malformed.

    v0.3.0 rules:
      - ``cloud`` must be False (cloud opt-in lands in v0.3.4).
      - ``name`` must be a non-empty identifier-shaped string.
      - ``recommended_model`` must be non-empty.
      - ``tools`` must be a list of strings.
      - ``max_tool_calls`` must be a non-negative int.
    """
    if skill.cloud:
        raise ValueError(
            f"skill {skill.name!r}: cloud=true is not permitted in v0.3.0 "
            "(cloud opt-in ships in v0.3.4)"
        )
    if not skill.name or not skill.name.strip():
        raise ValueError("skill: 'name' must be a non-empty string")
    if not skill.recommended_model.strip():
        raise ValueError(
            f"skill {skill.name!r}: 'recommended_model' must be non-empty"
        )
    if not isinstance(skill.tools, list) or not all(
        isinstance(t, str) for t in skill.tools
    ):
        raise ValueError(
            f"skill {skill.name!r}: 'tools' must be a list of strings"
        )
    if skill.max_tool_calls < 0:
        raise ValueError(
            f"skill {skill.name!r}: 'max_tool_calls' must be >= 0"
        )
    if not skill.system_prompt.strip():
        raise ValueError(
            f"skill {skill.name!r}: system prompt body is empty"
        )


__all__ = ["Skill", "load_skill", "load_all_skills", "validate_skill"]
