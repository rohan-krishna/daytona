// Copyright Daytona Platforms Inc.
// SPDX-License-Identifier: Apache-2.0

package io.daytona.sdk.model;

import com.fasterxml.jackson.annotation.JsonIgnoreProperties;
import com.fasterxml.jackson.annotation.JsonProperty;

import java.util.ArrayList;
import java.util.List;
import java.util.Map;

@JsonIgnoreProperties(ignoreUnknown = true)
public class ListSandboxesResponse {
    @JsonProperty("items")
    private List<Map<String, Object>> items;
    @JsonProperty("nextCursor")
    private String nextCursor;

    public List<Map<String, Object>> getItems() { return items == null ? new ArrayList<>() : items; }
    public void setItems(List<Map<String, Object>> items) { this.items = items; }
    public String getNextCursor() { return nextCursor; }
    public void setNextCursor(String nextCursor) { this.nextCursor = nextCursor; }
}
