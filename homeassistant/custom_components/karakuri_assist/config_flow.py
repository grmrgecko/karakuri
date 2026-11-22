"""Config flow for Karakuri Assist."""

from typing import Any, override

import voluptuous as vol

from homeassistant.config_entries import ConfigFlow, ConfigFlowResult
from homeassistant.const import CONF_NAME
from homeassistant.helpers.selector import (
    NumberSelector,
    NumberSelectorConfig,
    TextSelector,
)

from .const import (
    CONF_AGENT_ID,
    CONF_TIMEOUT,
    DEFAULT_AGENT_ID,
    DEFAULT_NAME,
    DEFAULT_TIMEOUT,
    DOMAIN,
)


def _schema(values: dict[str, Any] | None = None) -> vol.Schema:
    """Build the setup form with current values as suggestions."""
    values = values or {}
    return vol.Schema(
        {
            vol.Required(
                CONF_NAME, default=values.get(CONF_NAME, DEFAULT_NAME)
            ): TextSelector(),
            vol.Required(
                CONF_AGENT_ID,
                default=values.get(CONF_AGENT_ID, DEFAULT_AGENT_ID),
            ): TextSelector(),
            vol.Required(
                CONF_TIMEOUT, default=values.get(CONF_TIMEOUT, DEFAULT_TIMEOUT)
            ): NumberSelector(NumberSelectorConfig(min=5, max=600, step=1)),
        }
    )


class KarakuriAssistConfigFlow(ConfigFlow, domain=DOMAIN):
    """Configure a Karakuri Assist agent."""

    VERSION = 1

    @override
    async def async_step_user(
        self, user_input: dict[str, Any] | None = None
    ) -> ConfigFlowResult:
        """Create an agent from the UI."""
        errors: dict[str, str] = {}
        if user_input is not None:
            user_input[CONF_AGENT_ID] = user_input[CONF_AGENT_ID].strip()
            if not user_input[CONF_AGENT_ID]:
                errors[CONF_AGENT_ID] = "required"
            if not errors:
                self._async_abort_entries_match(
                    {CONF_AGENT_ID: user_input[CONF_AGENT_ID]}
                )
                return self.async_create_entry(
                    title=user_input[CONF_NAME], data=user_input
                )
        return self.async_show_form(
            step_id="user", data_schema=_schema(user_input), errors=errors
        )

    @override
    async def async_step_reconfigure(
        self, user_input: dict[str, Any] | None = None
    ) -> ConfigFlowResult:
        """Update the agent route or timeout."""
        entry = self._get_reconfigure_entry()
        errors: dict[str, str] = {}
        if user_input is not None:
            user_input[CONF_AGENT_ID] = user_input[CONF_AGENT_ID].strip()
            if not user_input[CONF_AGENT_ID]:
                errors[CONF_AGENT_ID] = "required"
            if not errors:
                return self.async_update_reload_and_abort(
                    entry, data=user_input, title=user_input[CONF_NAME]
                )
        return self.async_show_form(
            step_id="reconfigure",
            data_schema=_schema(user_input or dict(entry.data)),
            errors=errors,
        )
