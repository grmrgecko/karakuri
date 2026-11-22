"""Karakuri Assist integration."""

from typing import Any

import voluptuous as vol

from homeassistant.config_entries import ConfigEntry
from homeassistant.const import Platform
from homeassistant.core import HomeAssistant
from homeassistant.helpers import config_validation as cv

from .client import KarakuriAssistBroker, KarakuriAssistClient
from .const import (
    CONF_AGENT_ID,
    CONF_TIMEOUT,
    DEFAULT_AGENT_ID,
    DEFAULT_TIMEOUT,
    DOMAIN,
    SERVICE_RESPOND,
)

PLATFORMS = (Platform.CONVERSATION,)
CONFIG_SCHEMA = cv.config_entry_only_config_schema(DOMAIN)

type KarakuriAssistConfigEntry = ConfigEntry[KarakuriAssistClient]


async def async_setup(hass: HomeAssistant, config: dict[str, Any]) -> bool:
    """Register the response service shared by all Karakuri agents."""
    broker = KarakuriAssistBroker(hass)
    hass.data[DOMAIN] = broker
    hass.services.async_register(
        DOMAIN,
        SERVICE_RESPOND,
        broker.async_respond,
        schema=vol.Schema(
            {
                vol.Required("request_id"): str,
                vol.Required("agent_id"): str,
                vol.Required("response"): str,
                vol.Optional("stream", default=False): bool,
                vol.Optional("tool_call_id"): str,
                vol.Optional("tool_name"): str,
                vol.Optional("tool_label"): str,
                vol.Optional("tool_status"): vol.In(("running", "completed")),
                vol.Optional("conversation_id"): str,
                vol.Optional("session_id"): str,
                vol.Optional("transcript"): vol.Any(list, dict),
                vol.Optional("continue_conversation"): bool,
            }
        ),
    )
    return True


async def async_setup_entry(
    hass: HomeAssistant, entry: KarakuriAssistConfigEntry
) -> bool:
    """Set up a Karakuri conversation agent."""
    settings = {**entry.data, **entry.options}
    entry.runtime_data = KarakuriAssistClient(
        broker=hass.data[DOMAIN],
        agent_id=settings.get(CONF_AGENT_ID, DEFAULT_AGENT_ID),
        timeout=settings.get(CONF_TIMEOUT, DEFAULT_TIMEOUT),
    )
    await hass.config_entries.async_forward_entry_setups(entry, PLATFORMS)
    return True


async def async_unload_entry(
    hass: HomeAssistant, entry: KarakuriAssistConfigEntry
) -> bool:
    """Unload a Karakuri conversation agent."""
    return await hass.config_entries.async_unload_platforms(entry, PLATFORMS)
