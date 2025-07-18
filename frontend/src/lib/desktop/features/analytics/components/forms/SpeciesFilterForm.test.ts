import { describe, it, expect, vi } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/svelte';
import SpeciesFilterForm from './SpeciesFilterForm.svelte';

const defaultFilters = {
  timePeriod: 'all' as const,
  startDate: '',
  endDate: '',
  sortOrder: 'count_desc' as const,
  searchTerm: '',
};

describe('SpeciesFilterForm', () => {
  it('renders with basic props', () => {
    render(SpeciesFilterForm, {
      props: {
        filters: defaultFilters,
        filteredCount: 42,
        onSubmit: vi.fn(),
        onReset: vi.fn(),
        onExport: vi.fn(),
        onSearchInput: vi.fn(),
      },
    });

    expect(screen.getByText('Filter Data')).toBeInTheDocument();
    expect(screen.getByText('42 species')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Apply Filters' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Reset' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Export CSV' })).toBeInTheDocument();
  });

  it('displays time period options', () => {
    render(SpeciesFilterForm, {
      props: {
        filters: defaultFilters,
        filteredCount: 0,
        onSubmit: vi.fn(),
        onReset: vi.fn(),
        onExport: vi.fn(),
        onSearchInput: vi.fn(),
      },
    });

    const timePeriodSelect = screen.getAllByRole('combobox')[0];
    expect(timePeriodSelect).toBeInTheDocument();
  });

  it('displays sort order options', () => {
    render(SpeciesFilterForm, {
      props: {
        filters: defaultFilters,
        filteredCount: 0,
        onSubmit: vi.fn(),
        onReset: vi.fn(),
        onExport: vi.fn(),
        onSearchInput: vi.fn(),
      },
    });

    const sortSelect = screen.getAllByRole('combobox')[1];
    expect(sortSelect).toBeInTheDocument();
  });

  it('shows custom date fields when custom time period is selected', () => {
    const customFilters = {
      ...defaultFilters,
      timePeriod: 'custom' as const,
    };

    render(SpeciesFilterForm, {
      props: {
        filters: customFilters,
        filteredCount: 0,
        onSubmit: vi.fn(),
        onReset: vi.fn(),
        onExport: vi.fn(),
        onSearchInput: vi.fn(),
      },
    });

    expect(screen.getByLabelText('From')).toBeInTheDocument();
    expect(screen.getByLabelText('To')).toBeInTheDocument();
  });

  it('hides custom date fields when other time period is selected', () => {
    render(SpeciesFilterForm, {
      props: {
        filters: defaultFilters,
        filteredCount: 0,
        onSubmit: vi.fn(),
        onReset: vi.fn(),
        onExport: vi.fn(),
        onSearchInput: vi.fn(),
      },
    });

    expect(screen.queryByLabelText('From')).not.toBeInTheDocument();
    expect(screen.queryByLabelText('To')).not.toBeInTheDocument();
  });

  it('displays search input field', () => {
    render(SpeciesFilterForm, {
      props: {
        filters: defaultFilters,
        filteredCount: 0,
        onSubmit: vi.fn(),
        onReset: vi.fn(),
        onExport: vi.fn(),
        onSearchInput: vi.fn(),
      },
    });

    expect(screen.getByPlaceholderText('Search by name...')).toBeInTheDocument();
  });

  it('calls onSubmit when form is submitted', async () => {
    const onSubmit = vi.fn();

    render(SpeciesFilterForm, {
      props: {
        filters: defaultFilters,
        filteredCount: 0,
        onSubmit,
        onReset: vi.fn(),
        onExport: vi.fn(),
        onSearchInput: vi.fn(),
      },
    });

    const form = screen.getByRole('button', { name: 'Apply Filters' }).closest('form');
    if (form) {
      await fireEvent.submit(form);
    }

    expect(onSubmit).toHaveBeenCalled();
  });

  it('calls onReset when reset button is clicked', async () => {
    const onReset = vi.fn();

    render(SpeciesFilterForm, {
      props: {
        filters: defaultFilters,
        filteredCount: 0,
        onSubmit: vi.fn(),
        onReset,
        onExport: vi.fn(),
        onSearchInput: vi.fn(),
      },
    });

    const resetButton = screen.getByRole('button', { name: 'Reset' });
    await fireEvent.click(resetButton);

    expect(onReset).toHaveBeenCalled();
  });

  it('calls onExport when export button is clicked', async () => {
    const onExport = vi.fn();

    render(SpeciesFilterForm, {
      props: {
        filters: defaultFilters,
        filteredCount: 0,
        onSubmit: vi.fn(),
        onReset: vi.fn(),
        onExport,
        onSearchInput: vi.fn(),
      },
    });

    const exportButton = screen.getByRole('button', { name: 'Export CSV' });
    await fireEvent.click(exportButton);

    expect(onExport).toHaveBeenCalled();
  });

  it('calls onSearchInput when search field changes', async () => {
    const onSearchInput = vi.fn();

    render(SpeciesFilterForm, {
      props: {
        filters: defaultFilters,
        filteredCount: 0,
        onSubmit: vi.fn(),
        onReset: vi.fn(),
        onExport: vi.fn(),
        onSearchInput,
      },
    });

    const searchInput = screen.getByPlaceholderText('Search by name...');
    await fireEvent.input(searchInput, { target: { value: 'robin' } });

    expect(onSearchInput).toHaveBeenCalled();
  });

  it('disables buttons when loading', () => {
    render(SpeciesFilterForm, {
      props: {
        filters: defaultFilters,
        filteredCount: 0,
        isLoading: true,
        onSubmit: vi.fn(),
        onReset: vi.fn(),
        onExport: vi.fn(),
        onSearchInput: vi.fn(),
      },
    });

    expect(screen.getByRole('button', { name: 'Apply Filters' })).toBeDisabled();
    expect(screen.getByRole('button', { name: 'Reset' })).toBeDisabled();
    expect(screen.getByRole('button', { name: 'Export CSV' })).toBeDisabled();
  });

  it('shows loading spinner when loading', () => {
    render(SpeciesFilterForm, {
      props: {
        filters: defaultFilters,
        filteredCount: 0,
        isLoading: true,
        onSubmit: vi.fn(),
        onReset: vi.fn(),
        onExport: vi.fn(),
        onSearchInput: vi.fn(),
      },
    });

    const loadingSpinner = document.querySelector('.loading-spinner');
    expect(loadingSpinner).toBeInTheDocument();
  });

  it('shows filtered indicator when search term is present', () => {
    const searchFilters = {
      ...defaultFilters,
      searchTerm: 'robin',
    };

    render(SpeciesFilterForm, {
      props: {
        filters: searchFilters,
        filteredCount: 5,
        onSubmit: vi.fn(),
        onReset: vi.fn(),
        onExport: vi.fn(),
        onSearchInput: vi.fn(),
      },
    });

    expect(screen.getByText('5 species (filtered)')).toBeInTheDocument();
  });

  it('prevents default form submission', async () => {
    const onSubmit = vi.fn();

    render(SpeciesFilterForm, {
      props: {
        filters: defaultFilters,
        filteredCount: 0,
        onSubmit,
        onReset: vi.fn(),
        onExport: vi.fn(),
        onSearchInput: vi.fn(),
      },
    });

    const form = screen.getByRole('button', { name: 'Apply Filters' }).closest('form');
    const submitEvent = new Event('submit', { bubbles: true, cancelable: true });

    if (form) {
      await fireEvent(form, submitEvent);
    }

    expect(submitEvent.defaultPrevented).toBe(true);
    expect(onSubmit).toHaveBeenCalled();
  });

  it('renders with proper form structure and grid layout', () => {
    render(SpeciesFilterForm, {
      props: {
        filters: defaultFilters,
        filteredCount: 0,
        onSubmit: vi.fn(),
        onReset: vi.fn(),
        onExport: vi.fn(),
        onSearchInput: vi.fn(),
      },
    });

    const card = screen.getByText('Filter Data').closest('.card');
    expect(card).toHaveClass('bg-base-100', 'shadow-sm');

    const filtersGrid = document.querySelector('.filters-grid');
    expect(filtersGrid).toBeInTheDocument();
    expect(filtersGrid).toHaveStyle('display: grid');
  });

  it('has proper accessibility attributes', () => {
    render(SpeciesFilterForm, {
      props: {
        filters: defaultFilters,
        filteredCount: 0,
        onSubmit: vi.fn(),
        onReset: vi.fn(),
        onExport: vi.fn(),
        onSearchInput: vi.fn(),
      },
    });

    const heading = screen.getByRole('heading', { name: 'Filter Data' });
    expect(heading).toHaveAttribute('id', 'species-filters-heading');

    const form = document.querySelector('#speciesFiltersForm');
    expect(form).toBeInTheDocument();
  });

  it('displays export CSV icon', () => {
    render(SpeciesFilterForm, {
      props: {
        filters: defaultFilters,
        filteredCount: 0,
        onSubmit: vi.fn(),
        onReset: vi.fn(),
        onExport: vi.fn(),
        onSearchInput: vi.fn(),
      },
    });

    const exportButton = screen.getByRole('button', { name: 'Export CSV' });
    const svg = exportButton.querySelector('svg');
    expect(svg).toBeInTheDocument();
  });

  it('updates filter values correctly', async () => {
    const filters = { ...defaultFilters };

    render(SpeciesFilterForm, {
      props: {
        filters,
        filteredCount: 0,
        onSubmit: vi.fn(),
        onReset: vi.fn(),
        onExport: vi.fn(),
        onSearchInput: vi.fn(),
      },
    });

    const searchInput = screen.getByPlaceholderText('Search by name...');
    await fireEvent.input(searchInput, { target: { value: 'eagle' } });

    expect(searchInput).toHaveValue('eagle');
  });
});
