# frozen_string_literal: true

require 'daytona'

daytona = Daytona::Daytona.new

cursor = nil
loop do
  result = daytona.list(Daytona::ListSandboxesQuery.new(
                          cursor: cursor,
                          limit: 10,
                          labels: { 'env' => 'dev' },
                          states: ['started'],
                          sort: 'createdAt',
                          order: 'desc'
                        ))
  result.items.each { |sandbox| puts sandbox.id }
  cursor = result.next_cursor
  break unless cursor
end
