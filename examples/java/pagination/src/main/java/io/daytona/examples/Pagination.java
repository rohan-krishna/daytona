// Copyright Daytona Platforms Inc.
// SPDX-License-Identifier: Apache-2.0

package io.daytona.examples;

import io.daytona.sdk.Daytona;
import io.daytona.sdk.model.ListSandboxesQuery;
import io.daytona.sdk.model.ListSandboxesResponse;

import java.util.List;
import java.util.Map;

public class Pagination {
    public static void main(String[] args) {
        try (Daytona daytona = new Daytona()) {
            String cursor = null;
            do {
                ListSandboxesQuery query = new ListSandboxesQuery();
                query.setCursor(cursor);
                query.setLimit(10);
                query.setLabels(Map.of("env", "dev"));
                query.setStates(List.of("started"));
                query.setSort("createdAt");
                query.setOrder("desc");
                ListSandboxesResponse result = daytona.list(query);
                for (Map<String, Object> sandbox : result.getItems()) {
                    System.out.println(sandbox.get("id"));
                }
                cursor = result.getNextCursor();
            } while (cursor != null);
        }
    }
}
