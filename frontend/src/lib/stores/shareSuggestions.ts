import { api } from '../api/client';

interface SuggestedUser {
  id: string;
  username: string;
}

interface SuggestedGroup {
  id: number;
  name: string;
}

interface SuggestionResponse {
  users: SuggestedUser[];
  groups: SuggestedGroup[];
}

function createShareSuggestionsStore() {
  return {
    async getSuggestions(query?: string) {
      const params = query ? `?q=${encodeURIComponent(query)}` : '';
      return api.get<SuggestionResponse>(`/share_suggestions${params}`);
    },
  };
}

export const shareSuggestionsStore = createShareSuggestionsStore();
export type { SuggestedUser, SuggestedGroup, SuggestionResponse };
