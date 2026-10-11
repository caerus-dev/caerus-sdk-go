package sre

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"sync"
	"time"
)

const (
	defaultMockTTL      = 300 * time.Second
	defaultMockPageSize = 25
)

type MockMethod string

const (
	MockCreateUnitary       MockMethod = "createUnitary"
	MockCreateMultiple      MockMethod = "createMultiple"
	MockUpdateResource      MockMethod = "updateResource"
	MockDeleteResource      MockMethod = "deleteResource"
	MockTake                MockMethod = "take"
	MockTakeMany            MockMethod = "takeMany"
	MockConfirm             MockMethod = "confirm"
	MockRelease             MockMethod = "release"
	MockExtend              MockMethod = "extend"
	MockGetResource         MockMethod = "getResource"
	MockGetResourcesByGroup MockMethod = "getResourcesByGroup"
	MockGetResourceHolder   MockMethod = "getResourceHolder"
	MockListResourceHolders MockMethod = "listResourceHolders"
)

type MockResourceSeed struct {
	Key             string
	AvailableAmount int
	TemplateID      string
	GroupKey        string
	Metadata        map[string]any
}

type MockOptions struct {
	Resources       []MockResourceSeed
	DefaultTTL      time.Duration
	DefaultPageSize int
}

type MockSnapshot struct {
	Resources []Resource
	Holders   []ResourceHolder
}

type storedResource struct {
	id              string
	key             string
	templateID      string
	availableAmount int
	pendingCount    int
	groupKey        string
	metadata        map[string]any
	createdAt       time.Time
	updatedAt       time.Time
}

type storedHolder struct {
	id          string
	resourceKey string
	resourceID  string
	amount      int
	status      ResourceHolderStatus
	expiresAt   int64
	metadata    map[string]any
	createdAt   time.Time
}

type InMemoryClient struct {
	mu              sync.Mutex
	resources       map[string]*storedResource
	resourceOrder   []string
	holders         map[string]*storedHolder
	holderOrder     []string
	idempotency     map[string]string
	failures        map[MockMethod][]error
	defaultTTL      int64
	defaultPageSize int
	nowSeconds      int64
	sequence        int
	closed          bool
}

func NewInMemoryClient(options *MockOptions) *InMemoryClient {
	if options == nil {
		options = &MockOptions{}
	}
	client := &InMemoryClient{
		resources:       map[string]*storedResource{},
		holders:         map[string]*storedHolder{},
		idempotency:     map[string]string{},
		failures:        map[MockMethod][]error{},
		defaultTTL:      int64(defaultMockTTL / time.Second),
		defaultPageSize: defaultMockPageSize,
		nowSeconds:      time.Now().Unix(),
	}
	if options.DefaultTTL > 0 {
		client.defaultTTL = ceilDiv(options.DefaultTTL, time.Second)
	}
	if options.DefaultPageSize > 0 {
		client.defaultPageSize = options.DefaultPageSize
	}
	for _, seed := range options.Resources {
		templateID := seed.TemplateID
		if templateID == "" {
			templateID = "tpl-mock"
		}
		client.store(&storedResource{
			id:              client.nextID("res"),
			key:             seed.Key,
			templateID:      templateID,
			availableAmount: seed.AvailableAmount,
			groupKey:        seed.GroupKey,
			metadata:        maps.Clone(seed.Metadata),
			createdAt:       client.now(),
			updatedAt:       client.now(),
		})
	}
	return client
}

func (m *InMemoryClient) FailNext(method MockMethod, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.failures[method] = append(m.failures[method], err)
}

func (m *InMemoryClient) ClearFailures() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.failures = map[MockMethod][]error{}
}

func (m *InMemoryClient) AdvanceTime(elapsed time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if elapsed < 0 {
		return NewValidationError("AdvanceTime needs a duration that is not negative", nil)
	}
	m.nowSeconds += int64(elapsed / time.Second)
	for _, id := range m.holderOrder {
		holder := m.holders[id]
		if holder.status == StatusPending && holder.expiresAt <= m.nowSeconds {
			m.giveBack(holder, StatusExpired)
		}
	}
	return nil
}

func (m *InMemoryClient) Expire(resourceHolderID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	holder, err := m.requireHolder(resourceHolderID)
	if err != nil {
		return err
	}
	if holder.status != StatusPending {
		return NewHolderNotActiveError("Holder "+resourceHolderID+" is "+string(holder.status)+" and cannot expire.", nil)
	}
	m.giveBack(holder, StatusExpired)
	return nil
}

func (m *InMemoryClient) Snapshot() MockSnapshot {
	m.mu.Lock()
	defer m.mu.Unlock()
	snapshot := MockSnapshot{Resources: []Resource{}, Holders: []ResourceHolder{}}
	for _, key := range m.resourceOrder {
		snapshot.Resources = append(snapshot.Resources, *m.toResource(m.resources[key]))
	}
	for _, id := range m.holderOrder {
		snapshot.Holders = append(snapshot.Holders, *m.toHolder(m.holders[id]))
	}
	return snapshot
}

func (m *InMemoryClient) CreateUnitary(_ context.Context, templateName, key string, options *CreateResourceOptions) (*Resource, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.guard(MockCreateUnitary); err != nil {
		return nil, err
	}
	return m.createResource(templateName, key, 1, options)
}

func (m *InMemoryClient) CreateMultiple(_ context.Context, templateName, key string, availableAmount int, options *CreateResourceOptions) (*Resource, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.guard(MockCreateMultiple); err != nil {
		return nil, err
	}
	if err := requirePositive(int64(availableAmount), "availableAmount"); err != nil {
		return nil, err
	}
	return m.createResource(templateName, key, availableAmount, options)
}

func (m *InMemoryClient) createResource(templateName, key string, availableAmount int, options *CreateResourceOptions) (*Resource, error) {
	if err := requireText(templateName, "templateName"); err != nil {
		return nil, err
	}
	if err := requireText(key, "key"); err != nil {
		return nil, err
	}
	if options == nil {
		options = &CreateResourceOptions{}
	}
	if _, exists := m.resources[key]; exists {
		return nil, NewConflictError("A resource with key "+key+" already exists.", nil)
	}
	resource := &storedResource{
		id:              m.nextID("res"),
		key:             key,
		templateID:      "tpl-" + templateName,
		availableAmount: availableAmount,
		groupKey:        options.GroupKey,
		metadata:        maps.Clone(options.Metadata),
		createdAt:       m.now(),
		updatedAt:       m.now(),
	}
	m.store(resource)
	return m.toResource(resource), nil
}

func (m *InMemoryClient) UpdateResource(_ context.Context, key string, deltaAmount int, options *UpdateResourceOptions) (*Resource, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.guard(MockUpdateResource); err != nil {
		return nil, err
	}
	if err := requireText(key, "key"); err != nil {
		return nil, err
	}
	if options == nil {
		options = &UpdateResourceOptions{}
	}
	resource, err := m.requireResource(key)
	if err != nil {
		return nil, err
	}
	if resource.availableAmount+deltaAmount < 0 {
		return nil, NewConflictError("Available amount cannot be negative for resource: "+key, nil)
	}
	resource.availableAmount += deltaAmount
	if options.GroupKey != "" {
		resource.groupKey = options.GroupKey
	}
	if options.Metadata != nil {
		resource.metadata = maps.Clone(options.Metadata)
	}
	resource.updatedAt = m.now()
	return m.toResource(resource), nil
}

func (m *InMemoryClient) DeleteResource(_ context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.guard(MockDeleteResource); err != nil {
		return err
	}
	if err := requireText(key, "key"); err != nil {
		return err
	}
	resource, err := m.requireResource(key)
	if err != nil {
		return err
	}
	if resource.pendingCount > 0 {
		return NewResourceHasActiveHoldsError("Cannot delete resource with active holds: "+key, nil)
	}
	delete(m.resources, key)
	m.resourceOrder = slices.DeleteFunc(m.resourceOrder, func(existing string) bool { return existing == key })
	return nil
}

func (m *InMemoryClient) Unitary(key string) *UnitaryResource {
	return &UnitaryResource{key: key, take: func(_ context.Context, resourceKey string, amount int, options *TakeOptions) (*ResourceHolder, error) {
		m.mu.Lock()
		defer m.mu.Unlock()
		if err := m.guard(MockTake); err != nil {
			return nil, err
		}
		return m.take(resourceKey, amount, options)
	}}
}

func (m *InMemoryClient) Pooled(key string) *PooledResource {
	return &PooledResource{key: key, take: func(_ context.Context, resourceKey string, amount int, options *TakeOptions) (*ResourceHolder, error) {
		m.mu.Lock()
		defer m.mu.Unlock()
		method := MockTakeMany
		if amount == 1 {
			method = MockTake
		}
		if err := m.guard(method); err != nil {
			return nil, err
		}
		return m.take(resourceKey, amount, options)
	}}
}

func (m *InMemoryClient) Confirm(_ context.Context, resourceHolderID string, options *ConfirmOptions) (*ResourceHolder, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.guard(MockConfirm); err != nil {
		return nil, err
	}
	holder, err := m.requireHolder(resourceHolderID)
	if err != nil {
		return nil, err
	}
	if holder.status != StatusPending {
		return nil, NewHolderNotActiveError("Holder "+resourceHolderID+" is "+string(holder.status)+" and cannot be confirmed.", nil)
	}
	resource, err := m.requireResource(holder.resourceKey)
	if err != nil {
		return nil, err
	}
	resource.pendingCount -= holder.amount
	holder.status = StatusConfirmed
	if options != nil && options.Metadata != nil {
		holder.metadata = maps.Clone(options.Metadata)
	}
	return m.toHolder(holder), nil
}

func (m *InMemoryClient) Release(_ context.Context, resourceHolderID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.guard(MockRelease); err != nil {
		return err
	}
	holder, err := m.requireHolder(resourceHolderID)
	if err != nil {
		return err
	}
	if holder.status != StatusPending {
		return NewHolderNotActiveError("Holder "+resourceHolderID+" is "+string(holder.status)+" and cannot be released.", nil)
	}
	m.giveBack(holder, StatusReleased)
	return nil
}

func (m *InMemoryClient) Extend(_ context.Context, resourceHolderID string, extra time.Duration) (*ResourceHolder, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.guard(MockExtend); err != nil {
		return nil, err
	}
	millis := ceilDiv(extra, time.Millisecond)
	if err := requirePositive(millis, "extra"); err != nil {
		return nil, err
	}
	holder, err := m.requireHolder(resourceHolderID)
	if err != nil {
		return nil, err
	}
	if holder.status != StatusPending {
		return nil, NewHolderNotActiveError("Holder "+resourceHolderID+" is "+string(holder.status)+" and cannot be extended.", nil)
	}
	holder.expiresAt += (millis + 999) / 1000
	return m.toHolder(holder), nil
}

func (m *InMemoryClient) GetResourceHolder(_ context.Context, resourceHolderID string) (*ResourceHolder, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.guard(MockGetResourceHolder); err != nil {
		return nil, err
	}
	holder, err := m.requireHolder(resourceHolderID)
	if err != nil {
		return nil, err
	}
	return m.toHolder(holder), nil
}

func (m *InMemoryClient) GetResource(_ context.Context, key string) (*Resource, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.guard(MockGetResource); err != nil {
		return nil, err
	}
	if err := requireText(key, "key"); err != nil {
		return nil, err
	}
	resource, err := m.requireResource(key)
	if err != nil {
		return nil, err
	}
	return m.toResource(resource), nil
}

func (m *InMemoryClient) GetResourcesByGroup(_ context.Context, groupKey string, options *GetResourcesByGroupOptions) (*ResourcePage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.guard(MockGetResourcesByGroup); err != nil {
		return nil, err
	}
	if err := requireText(groupKey, "groupKey"); err != nil {
		return nil, err
	}
	if options == nil {
		options = &GetResourcesByGroupOptions{}
	}
	pageSize := options.PageSize
	if pageSize == 0 {
		pageSize = m.defaultPageSize
	}
	var matching []*storedResource
	for _, key := range m.resourceOrder {
		if resource := m.resources[key]; resource.groupKey == groupKey {
			matching = append(matching, resource)
		}
	}
	page := &ResourcePage{Resources: []Resource{}, HasNextPage: hasNextPage(len(matching), options.Page, pageSize)}
	for _, resource := range slice(matching, options.Page, pageSize) {
		page.Resources = append(page.Resources, *m.toResource(resource))
	}
	return page, nil
}

func (m *InMemoryClient) ListResourceHolders(_ context.Context, options *ListResourceHoldersOptions) (*ResourceHolderPage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.guard(MockListResourceHolders); err != nil {
		return nil, err
	}
	if options == nil {
		options = &ListResourceHoldersOptions{}
	}
	var matching []*storedHolder
	for _, id := range m.holderOrder {
		holder := m.holders[id]
		if options.ResourceKey != "" && holder.resourceKey != options.ResourceKey {
			continue
		}
		if options.Status != "" && holder.status != options.Status {
			continue
		}
		matching = append(matching, holder)
	}
	if options.Sort != OldestFirst {
		slices.Reverse(matching)
	}
	pageSize := options.PageSize
	if pageSize == 0 {
		pageSize = m.defaultPageSize
	}
	page := &ResourceHolderPage{Holders: []ResourceHolder{}, HasNextPage: hasNextPage(len(matching), options.Page, pageSize)}
	for _, holder := range slice(matching, options.Page, pageSize) {
		page.Holders = append(page.Holders, *m.toHolder(holder))
	}
	return page, nil
}

func (m *InMemoryClient) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
	return nil
}

func (m *InMemoryClient) take(resourceKey string, amount int, options *TakeOptions) (*ResourceHolder, error) {
	if options == nil {
		options = &TakeOptions{}
	}
	ttl := m.defaultTTL
	if options.TTL != 0 {
		ttl = ceilDiv(options.TTL, time.Second)
		if err := requirePositive(ttl, "TTL"); err != nil {
			return nil, err
		}
	}
	if options.IdempotencyKey != "" {
		if known, ok := m.idempotency[options.IdempotencyKey]; ok {
			replayed := m.holders[known]
			if replayed.status != StatusPending && replayed.status != StatusQueued {
				return nil, NewHolderNotActiveError("Caerus returned holder "+replayed.id+" as "+string(replayed.status)+", which this call cannot use.", nil)
			}
			return m.toHolder(replayed), nil
		}
	}
	resource, err := m.requireResource(resourceKey)
	if err != nil {
		return nil, err
	}
	if resource.availableAmount < amount {
		return nil, NewOutOfStockError("Out of stock for resource: "+resourceKey, nil)
	}
	resource.availableAmount -= amount
	resource.pendingCount += amount
	holder := &storedHolder{
		id:          m.nextID("hld"),
		resourceKey: resourceKey,
		resourceID:  resource.id,
		amount:      amount,
		status:      StatusPending,
		expiresAt:   m.nowSeconds + ttl,
		metadata:    maps.Clone(options.Metadata),
		createdAt:   m.now(),
	}
	m.holders[holder.id] = holder
	m.holderOrder = append(m.holderOrder, holder.id)
	if options.IdempotencyKey != "" {
		m.idempotency[options.IdempotencyKey] = holder.id
	}
	return m.toHolder(holder), nil
}

func (m *InMemoryClient) giveBack(holder *storedHolder, status ResourceHolderStatus) {
	if resource, ok := m.resources[holder.resourceKey]; ok {
		resource.availableAmount += holder.amount
		resource.pendingCount -= holder.amount
	}
	holder.status = status
}

func (m *InMemoryClient) guard(method MockMethod) error {
	if m.closed {
		return closedError("sre.InMemoryClient")
	}
	if queue := m.failures[method]; len(queue) > 0 {
		m.failures[method] = queue[1:]
		return queue[0]
	}
	return nil
}

func (m *InMemoryClient) requireResource(key string) (*storedResource, error) {
	resource, ok := m.resources[key]
	if !ok {
		return nil, NewResourceNotFoundError("Resource not found: "+key, nil)
	}
	return resource, nil
}

func (m *InMemoryClient) requireHolder(id string) (*storedHolder, error) {
	if err := requireText(id, "resourceHolderID"); err != nil {
		return nil, err
	}
	holder, ok := m.holders[id]
	if !ok {
		return nil, NewResourceNotFoundError("ResourceHolder not found: "+id, nil)
	}
	return holder, nil
}

func (m *InMemoryClient) store(resource *storedResource) {
	if _, exists := m.resources[resource.key]; !exists {
		m.resourceOrder = append(m.resourceOrder, resource.key)
	}
	m.resources[resource.key] = resource
}

func (m *InMemoryClient) toResource(resource *storedResource) *Resource {
	return &Resource{
		ID:              resource.id,
		Key:             resource.key,
		TemplateID:      resource.templateID,
		AvailableAmount: resource.availableAmount,
		PendingCount:    resource.pendingCount,
		GroupKey:        resource.groupKey,
		Metadata:        maps.Clone(resource.metadata),
		CreatedAt:       resource.createdAt,
		UpdatedAt:       resource.updatedAt,
	}
}

func (m *InMemoryClient) toHolder(holder *storedHolder) *ResourceHolder {
	return &ResourceHolder{
		ID:         holder.id,
		ResourceID: holder.resourceID,
		Status:     holder.status,
		Amount:     holder.amount,
		ExpiresAt:  time.Unix(holder.expiresAt, 0),
		Metadata:   maps.Clone(holder.metadata),
		CreatedAt:  holder.createdAt,
	}
}

func (m *InMemoryClient) now() time.Time {
	return time.Unix(m.nowSeconds, 0)
}

func (m *InMemoryClient) nextID(prefix string) string {
	m.sequence++
	return fmt.Sprintf("%s-%d", prefix, m.sequence)
}

func slice[T any](items []T, page, pageSize int) []T {
	start := page * pageSize
	if pageSize <= 0 || start < 0 || start >= len(items) {
		return nil
	}
	return items[start:min(len(items), start+pageSize)]
}

func hasNextPage(total, page, pageSize int) bool {
	return page*pageSize+pageSize < total
}
